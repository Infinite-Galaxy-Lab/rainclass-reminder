// Package scheduler 是编排层：把数据源、策略、持久化与推送串起来。
//
// 它不包含任何 API 细节（在 ykt 包）、不包含提醒规则（在 domain 包）、
// 也不包含通道细节（在 notify 包）——只负责"按什么顺序调用、失败怎么办"。
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/domain"
	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/notify"
	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/store"
)

// TaskSource 是任务数据源（由 ykt.Client 实现，测试可用假实现）。
type TaskSource interface {
	FetchAll(ctx context.Context, detailDays int) ([]domain.Task, any, error)
}

// Fetcher 是更贴合实际用法的数据源抽象：只要求返回任务。
type Fetcher interface {
	FetchTasks(ctx context.Context) ([]domain.Task, error)
}

// IntervalTier 是"距最近截止 ≤ Within 时，用 Interval 轮询"的一档。
//
// Within 为 0 表示兜底档（没有更近的任务时用它）。
type IntervalTier struct {
	Within   time.Duration
	Interval time.Duration
}

// Options 是调度器配置。
type Options struct {
	Interval     time.Duration
	Jitter       time.Duration
	IdleInterval time.Duration
	// IntervalTiers 按紧急程度分级调频（有序，Within 升序，末档 Within=0）。
	// 为空时退化为"Interval / RelaxedInterval"两档。
	IntervalTiers []IntervalTier
	// RelaxedInterval 是"近期无到期任务"时的放宽间隔（见 RelaxHours）。
	RelaxedInterval time.Duration
	// RelaxHours 表示：最近截止时间超过这么多小时，就用放宽间隔。
	RelaxHours  int
	ActiveStart int
	ActiveEnd   int
	DetailDays  int
	Policy      domain.PolicyConfig
	WebUIAddr   string

	Logger *slog.Logger
	Store  store.Repository
	Notify *notify.Registry
	Source Fetcher
	// Clock 允许注入假时间（时间旅行测试）。
	Clock func() time.Time
	// DryRun 为 true 时只记录不实际发送。
	DryRun bool
}

// Scheduler 是主循环。
type Scheduler struct {
	opts Options
	log  *slog.Logger
	// wake 用于被外部（如面板的"立即刷新"）提前唤醒。
	// 带缓冲 1，重复触发会合并成一次。
	wake chan struct{}
}

// New 构造调度器。
func New(opts Options) (*Scheduler, error) {
	if opts.Store == nil {
		return nil, fmt.Errorf("缺少 store")
	}
	if opts.Source == nil {
		return nil, fmt.Errorf("缺少数据源")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.Policy.Milestones.MaxLookahead == 0 {
		opts.Policy.Milestones = domain.DefaultMilestoneConfig()
	}
	if opts.Interval <= 0 {
		opts.Interval = 10 * time.Minute
	}
	return &Scheduler{opts: opts, log: opts.Logger, wake: make(chan struct{}, 1)}, nil
}

// Trigger 请求立即执行一轮（非阻塞，重复调用会合并）。
//
// 用途：面板上的「立即刷新」按钮。以前面板只往自己的缓冲通道写信号，
// 但没有任何消费者，点了等于没反应——这里是真正的唤醒入口。
func (s *Scheduler) Trigger() {
	select {
	case s.wake <- struct{}{}:
	default: // 已有一个待处理的唤醒，无需重复
	}
}

// CycleResult 是一轮的执行结果（供 CLI 打印与 Web UI 展示）。
type CycleResult struct {
	StartedAt  time.Time
	Fetched    int
	Due        int
	Sent       int
	Failed     int
	Suppressed int
	Deferred   int
	Tasks      []domain.Task
	Err        error
	// Bootstrapped 为 true 表示本轮是首次运行，历史提醒已被静默处理。
	Bootstrapped bool
}

// bootstrap 在首次运行时把早已过期的提醒标记为已处理。
//
// 新装用户第一次启动时数据库是空的，所有历史任务的过期里程碑都会被判定
// 为"该发未发"，一次性补发几十上百条"已截止"通知。
// 策略是把 fireAt 早于 now - grace 的直接记为 suppressed，
// 但保留最近 grace 窗口内的。
func (s *Scheduler) bootstrap(ctx context.Context, tasks []domain.Task, now time.Time) (int, error) {
	const grace = 24 * time.Hour

	marked := 0
	for _, t := range tasks {
		for _, ms := range domain.MilestonesFor(t, s.opts.Policy.Milestones) {
			if ms.FireAt.After(now.Add(-grace)) {
				continue // 最近一天内的不跳过
			}
			key := ms.Key()
			ok, err := s.opts.Store.Claim(ctx, key)
			if err != nil {
				return marked, err
			}
			if !ok {
				continue
			}
			if err := s.opts.Store.Record(ctx, store.PushedRecord{
				Key:       key,
				TaskID:    t.ID,
				TaskTitle: t.Title,
				Kind:      string(ms.Kind),
				Offset:    ms.OffsetMinutes,
				FireAt:    ms.FireAt,
				SentAt:    now,
				Channel:   "",
				Status:    "suppressed",
				Error:     "bootstrap: 历史提醒已静默",
			}); err != nil {
				return marked, err
			}
			marked++
		}
	}
	return marked, nil
}

// RunCycle 执行一轮：抓取 → 决策 → 发送 → 记账。
//
// 这是系统的核心流程，所有副作用都在这里发生。
func (s *Scheduler) RunCycle(ctx context.Context) CycleResult {
	res := CycleResult{StartedAt: s.opts.Clock()}

	// 1) 抓取任务。失败时退回上次快照，保证 UI 可用、且不误判"任务消失"。
	tasks, err := s.opts.Source.FetchTasks(ctx)
	if err != nil {
		s.log.Warn("抓取任务失败", "err", err)
		res.Err = err
		snap, loadErr := s.opts.Store.LoadTasks(ctx)
		if loadErr != nil {
			return res
		}
		tasks = snap.Tasks
		if len(tasks) == 0 {
			return res
		}
		s.log.Info("使用上次任务快照继续判断", "count", len(tasks))
	} else {
		if err := s.opts.Store.SaveTasks(ctx, tasks); err == nil {
			if sv, ok := s.opts.Store.(interface {
				SaveSnapshotTime(context.Context, time.Time) error
			}); ok {
				_ = sv.SaveSnapshotTime(ctx, res.StartedAt)
			}
		}
	}
	res.Fetched = len(tasks)
	res.Tasks = tasks

	// 2) 读取已发送键，计算本轮该发什么。
	pushed, err := s.opts.Store.PushedKeys(ctx)
	if err != nil {
		res.Err = err
		return res
	}

	now := s.opts.Clock()

	// 2.1) 首次运行：静默处理历史提醒，避免开机补发上百条"已截止"。
	if len(pushed) == 0 {
		marked, err := s.bootstrap(ctx, tasks, now)
		if err != nil {
			s.log.Warn("首次运行初始化失败", "err", err)
		} else if marked > 0 {
			s.log.Info("首次运行：已静默历史提醒", "count", marked)
			res.Bootstrapped = true
			res.Suppressed += marked
		}
		// 重新读取，让本轮跳过已标记的项。
		if pushed, err = s.opts.Store.PushedKeys(ctx); err != nil {
			res.Err = err
			return res
		}
	}

	reminders := domain.Decide(now, tasks, pushed, s.opts.Policy)
	res.Due = len(reminders)

	// 3) 分类：顺延 / 抑制 / 待发送。
	var toSend []domain.Reminder
	for _, r := range reminders {
		switch {
		case r.Reason == "quiet_hours_deferred":
			res.Deferred++
		case r.Suppressed:
			res.Suppressed++
		default:
			toSend = append(toSend, r)
		}
	}

	// 3.1) 被抑制的里程碑也要记账，否则它们会在后续每一轮反复成为候选，
	//      导致"每次轮询都提醒一次"。这是"降噪"真正生效的关键一步。
	for _, r := range reminders {
		if !r.Suppressed {
			continue
		}
		key := r.Milestone.Key()
		ok, err := s.opts.Store.Claim(ctx, key)
		if err != nil {
			s.log.Error("占用被抑制提醒键失败", "key", key, "err", err)
			continue
		}
		if ok {
			_ = s.record(ctx, r, "", "suppressed", r.Reason)
		}
	}

	// 4) 发送并记账。
	for _, r := range toSend {
		key := r.Milestone.Key()
		ok, err := s.opts.Store.Claim(ctx, key)
		if err != nil {
			s.log.Error("占用提醒键失败", "key", key, "err", err)
			res.Failed++
			continue
		}
		if !ok {
			continue // 已被其它进程发过
		}

		msg := notify.RenderReminder(r, s.opts.Clock)
		if s.opts.DryRun {
			s.log.Info("[dry-run] 将发送", "title", msg.Title)
			res.Sent++
			_ = s.record(ctx, r, "dry-run", "sent", "")
			continue
		}

		results, err := s.opts.Notify.Send(ctx, msg)
		channel := ""
		if len(results) > 0 {
			channel = results[0].Channel
		}
		if err != nil {
			s.log.Error("发送提醒失败", "title", msg.Title, "err", err)
			// 先记录失败，再释放占位——顺序很重要：
			// Record 会把行更新为 failed，Release 只删除 claimed 行；
			// 若先 Release 再 Record，会重新插入一条 failed 记录（无害但冗余）。
			_ = s.record(ctx, r, channel, "failed", err.Error())
			_ = s.opts.Store.Release(ctx, key)
			res.Failed++
			continue
		}
		s.log.Info("已发送提醒", "title", msg.Title, "channel", channel)
		res.Sent++
		_ = s.record(ctx, r, channel, "sent", "")
	}

	return res
}

// record 写入一条推送记录。
func (s *Scheduler) record(ctx context.Context, r domain.Reminder, channel, status, errMsg string) error {
	return s.opts.Store.Record(ctx, store.PushedRecord{
		Key:       r.Milestone.Key(),
		TaskID:    r.Task.ID,
		TaskTitle: r.Task.Title,
		Kind:      string(r.Milestone.Kind),
		Offset:    r.Milestone.OffsetMinutes,
		FireAt:    r.Milestone.FireAt,
		SentAt:    s.opts.Clock(),
		Channel:   channel,
		Status:    status,
		Error:     errMsg,
	})
}

// AlertAuthExpired 在登录失效时发一次性告警（同状态不重复打扰）。
func (s *Scheduler) AlertAuthExpired(ctx context.Context) error {
	const kvKey = "auth_expired_alerted"

	prev, err := s.opts.Store.GetKV(ctx, kvKey)
	if err != nil {
		return err
	}
	if prev == "1" {
		return nil // 已告警过
	}
	if s.opts.Notify.Empty() {
		return nil
	}

	msg := notify.RenderAuthExpired(s.opts.WebUIAddr)
	if s.opts.DryRun {
		s.log.Info("[dry-run] 将发送登录失效告警")
	} else if _, err := s.opts.Notify.Send(ctx, msg); err != nil {
		return err
	}
	return s.opts.Store.SetKV(ctx, kvKey, "1")
}

// ClearAuthExpired 在登录恢复后清除告警标记，使下次失效时能再次告警。
func (s *Scheduler) ClearAuthExpired(ctx context.Context) {
	_ = s.opts.Store.SetKV(ctx, "auth_expired_alerted", "0")
}

// Run 启动主循环，直到 ctx 取消。
func (s *Scheduler) Run(ctx context.Context) error {
	s.log.Info("调度器启动",
		"interval", s.opts.Interval,
		"active", fmt.Sprintf("%02d:00-%02d:00", s.opts.ActiveStart, s.opts.ActiveEnd),
		"channels", s.opts.Notify.Names())

	for {
		res := s.RunCycle(ctx)
		s.logSummary(res)

		wait := s.nextWait(res.Tasks)
		s.log.Debug("等待下一轮", "wait", wait.String())

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.log.Info("调度器停止")
			return ctx.Err()
		case <-s.wake:
			// 面板点了「立即刷新」：不等了，立刻跑下一轮。
			timer.Stop()
			s.log.Info("收到手动刷新请求，提前执行下一轮")
		case <-timer.C:
		}
	}
}

// logSummary 输出一轮的概要。
func (s *Scheduler) logSummary(res CycleResult) {
	attrs := []any{
		"fetched", res.Fetched,
		"due", res.Due,
		"sent", res.Sent,
		"failed", res.Failed,
		"suppressed", res.Suppressed,
		"deferred", res.Deferred,
	}
	if res.Err != nil {
		attrs = append(attrs, "err", res.Err.Error())
	}
	s.log.Info("本轮完成", attrs...)
}

// nextWait 计算下一轮等待时间。
//
// 优先级（从上到下）：
//  1. 非活跃时段（夜间）→ IdleInterval，最省；
//  2. 按"距最近截止还剩多久"查 IntervalTiers —— 这是最有效的降频手段：
//     截止在 40 小时后时，每 20 分钟扫和每 3 分钟扫，提醒效果没区别，
//     但请求量差 6 倍；
//  3. 没有可用的分级表时退回 Interval / RelaxedInterval 两档。
//
// 无论走哪档都叠加随机抖动，避免固定节奏被识别为机器人。
func (s *Scheduler) nextWait(tasks []domain.Task) time.Duration {
	now := s.opts.Clock()
	base := s.tieredInterval(tasks, now)

	if !inActiveHours(now, s.opts.ActiveStart, s.opts.ActiveEnd) {
		if idle := s.opts.IdleInterval; idle > 0 {
			base = idle
		}
	}

	if s.opts.Jitter > 0 {
		// 增减一半抖动，避免总是"整点"请求。
		j := time.Duration(rand.Int63n(int64(s.opts.Jitter))) - s.opts.Jitter/2
		base += j
	}
	if base < 30*time.Second {
		base = 30 * time.Second
	}
	return base
}

// tieredInterval 按紧急程度选轮询间隔。
func (s *Scheduler) tieredInterval(tasks []domain.Task, now time.Time) time.Duration {
	nearest := nearestDeadline(tasks, now)

	// 有分级表：按"距最近截止还剩多久"命中对应档。
	if len(s.opts.IntervalTiers) > 0 {
		if nearest.IsZero() {
			// 没有任何将来截止的任务：用最宽一档（末档 Within=0）。
			if v := s.fallbackTier(); v > 0 {
				return v
			}
			return s.opts.Interval
		}
		left := nearest.Sub(now)
		for _, t := range s.opts.IntervalTiers {
			if t.Within > 0 && left <= t.Within {
				return t.Interval
			}
		}
		if v := s.fallbackTier(); v > 0 {
			return v
		}
		return s.opts.Interval
	}

	// 无分级表：退回两档逻辑（远期放宽）。
	if !nearest.IsZero() &&
		nearest.Sub(now) > time.Duration(s.opts.RelaxHours)*time.Hour &&
		s.opts.RelaxedInterval > 0 {
		return s.opts.RelaxedInterval
	}
	return s.opts.Interval
}

// fallbackTier 返回分级表里 Within=0 的兜底档，没有则返回 0。
func (s *Scheduler) fallbackTier() time.Duration {
	for _, t := range s.opts.IntervalTiers {
		if t.Within == 0 {
			return t.Interval
		}
	}
	return 0
}

// nearestDeadline 返回未来最近的一个截止时间；没有则返回零值。
//
// 只考虑"将来"的截止：已过期的任务不该让调度器继续高频跑。
func nearestDeadline(tasks []domain.Task, now time.Time) time.Time {
	var best time.Time
	for _, t := range tasks {
		if !t.HasDeadline() || t.Deadline.IsZero() {
			continue
		}
		if !t.Deadline.After(now) {
			continue
		}
		if best.IsZero() || t.Deadline.Before(best) {
			best = t.Deadline
		}
	}
	return best
}

// inActiveHours 判断是否处于活跃时段，支持跨午夜。
func inActiveHours(t time.Time, start, end int) bool {
	if start == end {
		return true
	}
	h := t.Hour()
	if start < end {
		return h >= start && h < end
	}
	return h >= start || h < end
}

// ErrNoTasks 表示没有任何任务（不算错误，仅用于日志区分）。
var ErrNoTasks = errors.New("没有任务")
