package domain

import (
	"fmt"
	"sort"
	"time"
)

// MilestoneKind 区分提醒的语义，决定文案与降级优先级。
type MilestoneKind string

const (
	KindBeforeDeadline MilestoneKind = "before_deadline" // 截止前 N 分钟
	KindExpired        MilestoneKind = "expired"         // 刚刚过期
	KindClassStarting  MilestoneKind = "class_starting"  // 课堂即将开始
)

// Milestone 是一个"提醒时间点"：在某任务的某个时刻，触发某类提醒。
type Milestone struct {
	TaskID string
	Kind   MilestoneKind
	// OffsetMinutes 是相对截止（或开课）提前的分钟数；过期提醒为 0。
	OffsetMinutes int
	// FireAt 是应当触发提醒的绝对时间。
	FireAt time.Time
	// Deadline 冗余保存，用于构造去重键（截止时间改动即视为新提醒）。
	Deadline time.Time
}

// Key 是持久化去重键。包含截止时间，因此老师改期会自动产生新键，
// 从而允许重新提醒——这是"改期后应重新倒数"的正确语义。
func (m Milestone) Key() string {
	// 不引入 hash 依赖，直接用可读字符串；SQLite 主键足够。
	if m.Deadline.IsZero() {
		return fmt.Sprintf("%s|%s|%d", m.TaskID, m.Kind, m.OffsetMinutes)
	}
	return fmt.Sprintf("%s|%s|%d|%d", m.TaskID, m.Kind, m.OffsetMinutes, m.Deadline.Unix())
}

// Label 返回里程碑的中文描述，用于通知标题。
func (m Milestone) Label() string {
	switch m.Kind {
	case KindExpired:
		return "已到截止时间"
	case KindClassStarting:
		return fmt.Sprintf("%d 分钟后开课", m.OffsetMinutes)
	default:
		if m.OffsetMinutes%1440 == 0 && m.OffsetMinutes >= 1440 {
			return fmt.Sprintf("还有 %d 天截止", m.OffsetMinutes/1440)
		}
		if m.OffsetMinutes%60 == 0 && m.OffsetMinutes >= 60 {
			return fmt.Sprintf("还有 %d 小时截止", m.OffsetMinutes/60)
		}
		return fmt.Sprintf("还有 %d 分钟截止", m.OffsetMinutes)
	}
}

// MilestoneConfig 控制提醒窗口。零值由 DefaultMilestoneConfig 补齐。
type MilestoneConfig struct {
	// BeforeDeadline 是截止前提醒窗口（分钟），例如 [1440, 360, 60, 15]。
	BeforeDeadline []int
	// ClassLeadMinutes 是课堂开课提前提醒窗口（分钟）。
	ClassLeadMinutes int
	// NotifyExpired 控制是否在截止后补发一次"已过期"。
	NotifyExpired bool
	// MaxLookahead 限制只看多远之内的截止，避免为半年后的任务做无谓计算。
	MaxLookahead time.Duration
}

// DefaultMilestoneConfig 返回产品默认值：1天/6小时/1小时/15分钟。
func DefaultMilestoneConfig() MilestoneConfig {
	return MilestoneConfig{
		BeforeDeadline:   []int{1440, 360, 60, 15},
		ClassLeadMinutes: 10,
		NotifyExpired:    true,
		MaxLookahead:     30 * 24 * time.Hour,
	}
}

// normalize 补齐零值并排序去重，保证行为稳定可测。
func (c MilestoneConfig) normalize() MilestoneConfig {
	if len(c.BeforeDeadline) == 0 {
		c.BeforeDeadline = DefaultMilestoneConfig().BeforeDeadline
	}
	if c.ClassLeadMinutes <= 0 {
		c.ClassLeadMinutes = DefaultMilestoneConfig().ClassLeadMinutes
	}
	if c.MaxLookahead <= 0 {
		c.MaxLookahead = DefaultMilestoneConfig().MaxLookahead
	}
	seen := make(map[int]bool, len(c.BeforeDeadline))
	out := make([]int, 0, len(c.BeforeDeadline))
	for _, m := range c.BeforeDeadline {
		if m <= 0 || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	// 从远到近排序：先 1440，再 360，再 60，再 15。
	sort.Sort(sort.Reverse(sort.IntSlice(out)))
	c.BeforeDeadline = out
	return c
}

// MilestonesFor 计算某任务的全部提醒时间点。
//
// 规则：
//   - 已完成的任务不产生任何提醒；
//   - 有截止时间的任务，按窗口生成 before_deadline，并按需生成 expired；
//   - 课堂类任务额外生成"开课前 ClassLeadMinutes"提醒；
//   - 超出 MaxLookahead 的提醒丢弃（不影响后续轮次重新纳入）。
func MilestonesFor(t Task, cfg MilestoneConfig) []Milestone {
	cfg = cfg.normalize()
	if t.Status == StatusDone {
		return nil
	}

	var out []Milestone
	if t.HasDeadline() && t.Type.HasDeadline() {
		for _, m := range cfg.BeforeDeadline {
			fireAt := t.Deadline.Add(-time.Duration(m) * time.Minute)
			out = append(out, Milestone{
				TaskID:        t.ID,
				Kind:          KindBeforeDeadline,
				OffsetMinutes: m,
				FireAt:        fireAt,
				Deadline:      t.Deadline,
			})
		}
		if cfg.NotifyExpired {
			out = append(out, Milestone{
				TaskID:        t.ID,
				Kind:          KindExpired,
				OffsetMinutes: 0,
				FireAt:        t.Deadline,
				Deadline:      t.Deadline,
			})
		}
	}

	if t.Type == TaskClass && !t.StartAt.IsZero() {
		lead := cfg.ClassLeadMinutes
		out = append(out, Milestone{
			TaskID:        t.ID,
			Kind:          KindClassStarting,
			OffsetMinutes: lead,
			FireAt:        t.StartAt.Add(-time.Duration(lead) * time.Minute),
			Deadline:      t.StartAt,
		})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].FireAt.Before(out[j].FireAt) })
	return out
}
