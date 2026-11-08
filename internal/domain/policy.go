package domain

import (
	"sort"
	"time"
)

// Reminder 是一条已决意要发送的提醒。
type Reminder struct {
	Task      Task
	Milestone Milestone
	// Urgency 越小越紧急（0 = 已过期/最后时刻）。用于降级时挑最紧急的一条。
	Urgency int
	// FireAt 是计划发送时间（可能因静默时段被顺延）。
	FireAt time.Time
	// Suppressed 为 true 表示本应发送但因降噪被合并/丢弃。
	Suppressed bool
	// Reason 记录 suppression 原因，便于日志排障。
	Reason string
}

// PolicyConfig 是提醒策略的可调参数。
type PolicyConfig struct {
	Milestones MilestoneConfig
	// QuietHours 为静默时段（本地时间，小时）。Start==End 表示关闭。
	QuietStartHour int
	QuietEndHour   int
	// QuietDeferTo 是静默时段结束后顺延到的时刻（小时:分钟）。
	QuietDeferHour   int
	QuietDeferMinute int
	// UrgentMinutes 之内的提醒不受静默时段影响（例如 15 分钟内截止）。
	UrgentMinutes int
	// Coalesce 为 true 时，同一轮内多条提醒只发最紧急的一条，其余标记 suppressed。
	Coalesce bool
	// MaxPerCycle 是单轮最多发送条数（Coalesce=false 时生效）。
	MaxPerCycle int
}

// DefaultPolicyConfig 返回默认策略：23:00-07:00 静默，顺延到 07:30，15 分钟内不静默。
func DefaultPolicyConfig() PolicyConfig {
	return PolicyConfig{
		Milestones:       DefaultMilestoneConfig(),
		QuietStartHour:   23,
		QuietEndHour:     7,
		QuietDeferHour:   7,
		QuietDeferMinute: 30,
		UrgentMinutes:    15,
		Coalesce:         true,
		MaxPerCycle:      8,
	}
}

// inQuietHours 判断 now（本地时区）是否落在静默区间，支持跨午夜。
func (c PolicyConfig) inQuietHours(now time.Time) bool {
	if c.QuietStartHour == c.QuietEndHour {
		return false
	}
	h := now.Hour()
	if c.QuietStartHour < c.QuietEndHour {
		return h >= c.QuietStartHour && h < c.QuietEndHour
	}
	// 跨午夜，例如 23 -> 7
	return h >= c.QuietStartHour || h < c.QuietEndHour
}

// deferTarget 返回静默结束后应当发送的时刻。
func (c PolicyConfig) deferTarget(now time.Time) time.Time {
	y, m, d := now.Date()
	target := time.Date(y, m, d, c.QuietDeferHour, c.QuietDeferMinute, 0, 0, now.Location())
	// 若静默区间跨午夜且当前在午夜之后，顺延点仍在"今天"。
	if !target.After(now) {
		target = target.Add(24 * time.Hour)
	}
	return target
}

// Decide 计算在 now 时刻应当发送哪些提醒。
//
// sent 是"已经发送过的里程碑键"集合（由 store 提供）。
// 输出只包含 fireAt <= now 的到期里程碑，即"该发了但还没发"的集合。
//
// 处理顺序：
//  1. 过滤已完成、无里程碑、未到点、已发送的项；
//  2. 静默时段内，非紧急项顺延（返回时 FireAt 被改写，仍返回给调用方记录）；
//  3. Coalesce 打开时，每轮每个任务只保留最紧急一条，并限制总数。
//
// 该函数不产生副作用，不访问 store，便于时间旅行测试。
func Decide(now time.Time, tasks []Task, sent map[string]bool, cfg PolicyConfig) []Reminder {
	if cfg.Milestones.MaxLookahead == 0 {
		cfg.Milestones = DefaultMilestoneConfig()
	}

	var due []Reminder
	for _, t := range tasks {
		for _, ms := range MilestonesFor(t, cfg.Milestones) {
			if sent[ms.Key()] {
				continue // 已发送过
			}
			// MaxLookahead 语义是"距现在多久以内的触发点才值得提醒"，
			// 因此必须在拿到 now 之后判断，而不是在 MilestonesFor 里。
			// 同时设置前后边界：太久以前错过的提醒（如离线两个月后开机）
			// 也不应补发，否则会形成无意义的噪声。
			if ms.FireAt.After(now.Add(cfg.Milestones.MaxLookahead)) {
				continue // 太远期，本轮不纳入
			}
			if ms.FireAt.Before(now.Add(-cfg.Milestones.MaxLookahead)) {
				continue // 太久远，已无提醒价值
			}
			if ms.FireAt.After(now) {
				continue // 未到点
			}
			r := Reminder{Task: t, Milestone: ms, FireAt: ms.FireAt, Urgency: urgencyOf(ms)}

			// 静默时段：紧急项照发，其它顺延。
			if cfg.inQuietHours(now) && !isUrgent(ms, cfg.UrgentMinutes) {
				r.FireAt = cfg.deferTarget(now)
				r.Reason = "quiet_hours_deferred"
				// 顺延项不进入本轮发送，但仍返回给调用方记账（不 Claim）。
				due = append(due, r)
				continue
			}
			due = append(due, r)
		}
	}

	// 最紧急优先。
	sort.SliceStable(due, func(i, j int) bool {
		if due[i].Urgency != due[j].Urgency {
			return due[i].Urgency < due[j].Urgency
		}
		return due[i].FireAt.Before(due[j].FireAt)
	})

	if !cfg.Coalesce {
		if cfg.MaxPerCycle > 0 && len(due) > cfg.MaxPerCycle {
			for i := cfg.MaxPerCycle; i < len(due); i++ {
				due[i].Suppressed = true
				due[i].Reason = "max_per_cycle"
			}
		}
		return due
	}

	// Coalesce：每个任务只保留最紧急一条；其余标记为 merged（已处理但未单独发送）。
	seenTask := make(map[string]bool, len(due))
	kept := make([]Reminder, 0, len(due))
	for _, r := range due {
		if r.Reason == "quiet_hours_deferred" {
			kept = append(kept, r)
			continue
		}
		if seenTask[r.Task.ID] {
			r.Suppressed = true
			r.Reason = "coalesced"
			kept = append(kept, r)
			continue
		}
		seenTask[r.Task.ID] = true
		kept = append(kept, r)
	}

	// 统计真正会发送的条数，超限则压制。
	sentCount := 0
	for i := range kept {
		if kept[i].Suppressed || kept[i].Reason != "" {
			continue
		}
		sentCount++
		if cfg.MaxPerCycle > 0 && sentCount > cfg.MaxPerCycle {
			kept[i].Suppressed = true
			kept[i].Reason = "max_per_cycle"
		}
	}
	return kept
}

// MarkHandled 返回本轮"应当记账为已处理"的提醒键。
//
// 语义：被发送的、被 coalesce 合并的、被配额压制的，都算"已经处理过"——
// 否则它们会在下一轮反复出现在候选集里，造成重复提醒。
// 只有被静默时段顺延的项必须保持未处理状态，等顺延时刻再发。
//
// 调用方应在发送后对每个键调用 store.Record，未发送的记为 suppressed。
func MarkHandled(reminders []Reminder) []Reminder {
	out := make([]Reminder, 0, len(reminders))
	for _, r := range reminders {
		if r.Reason == "quiet_hours_deferred" {
			continue // 保持待发送
		}
		out = append(out, r)
	}
	return out
}

// urgencyOf 把里程碑映射为紧急度：过期最紧急，其次窗口越小越紧急。
func urgencyOf(m Milestone) int {
	if m.Kind == KindExpired {
		return 0
	}
	// 15min -> 15, 60 -> 60 ... 数字越小越紧急
	return m.OffsetMinutes
}

// isUrgent 判断是否属于"必须立刻发出、不受静默影响"的提醒。
func isUrgent(m Milestone, urgentMinutes int) bool {
	if m.Kind == KindExpired {
		return true
	}
	return m.OffsetMinutes <= urgentMinutes
}

// Sendable 过滤出真正要发送的提醒（未被抑制且原因非顺延）。
func Sendable(reminders []Reminder) []Reminder {
	out := make([]Reminder, 0, len(reminders))
	for _, r := range reminders {
		if r.Suppressed || r.Reason != "" {
			continue
		}
		out = append(out, r)
	}
	return out
}
