package scheduler

import (
	"testing"
	"time"

	"github.com/2963609262/ykt-reminder/internal/domain"
)

// TestNearestDeadline 验证"最近未来截止"的挑选逻辑。
func TestNearestDeadline(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.Local)

	tasks := []domain.Task{
		// 已经过期的：不该影响节奏判断
		{ID: "a", Type: domain.TaskHomework, Deadline: now.Add(-2 * time.Hour)},
		// 5 天后
		{ID: "b", Type: domain.TaskHomework, Deadline: now.Add(5 * 24 * time.Hour)},
		// 90 分钟后，是最近的
		{ID: "c", Type: domain.TaskExam, Deadline: now.Add(90 * time.Minute)},
		// 视频没有截止概念
		{ID: "d", Type: domain.TaskVideo},
	}

	got := nearestDeadline(tasks, now)
	if !got.Equal(now.Add(90 * time.Minute)) {
		t.Fatalf("应选中 90 分钟后的截止，实际 %v", got)
	}
}

// TestNearestDeadlineNone 全部过期/无截止时应返回零值。
func TestNearestDeadlineNone(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.Local)
	tasks := []domain.Task{
		{ID: "a", Type: domain.TaskHomework, Deadline: now.Add(-time.Hour)},
		{ID: "b", Type: domain.TaskVideo},
	}
	if got := nearestDeadline(tasks, now); !got.IsZero() {
		t.Fatalf("应返回零值，实际 %v", got)
	}
}

// TestNextWaitRelaxes 验证"远期无任务时降频"。
func TestNextWaitRelaxes(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.Local)
	s := &Scheduler{opts: Options{
		Interval:        10 * time.Minute,
		RelaxedInterval: 30 * time.Minute,
		RelaxHours:      6,
		ActiveStart:     7,
		ActiveEnd:       23,
		Clock:           func() time.Time { return now },
	}}

	// 最近截止在 3 天后 → 放宽到 30m
	far := []domain.Task{{ID: "x", Type: domain.TaskHomework, Deadline: now.Add(72 * time.Hour)}}
	if got := s.nextWait(far); got != 30*time.Minute {
		t.Fatalf("远期任务应放宽到 30m，实际 %v", got)
	}

	// 最近截止在 2 小时后 → 保持 10m
	near := []domain.Task{{ID: "y", Type: domain.TaskHomework, Deadline: now.Add(2 * time.Hour)}}
	if got := s.nextWait(near); got != 10*time.Minute {
		t.Fatalf("临近任务应保持 10m，实际 %v", got)
	}
}

// TestNextWaitIdleHours 验证夜间用 IdleInterval。
func TestNextWaitIdleHours(t *testing.T) {
	night := time.Date(2026, 11, 7, 3, 0, 0, 0, time.Local)
	s := &Scheduler{opts: Options{
		Interval:     10 * time.Minute,
		IdleInterval: 60 * time.Minute,
		ActiveStart:  7,
		ActiveEnd:    23,
		Clock:        func() time.Time { return night },
	}}
	if got := s.nextWait(nil); got != 60*time.Minute {
		t.Fatalf("夜间应为 60m，实际 %v", got)
	}
}

// TestNextWaitTiers 验证按紧急程度分级调频。
//
// 这是控制请求量最有效的一环：间隔由"离下一个截止还有多久"决定，
// 而不是固定值。
func TestNextWaitTiers(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.Local)
	s := &Scheduler{opts: Options{
		ActiveStart: 7,
		ActiveEnd:   23,
		Clock:       func() time.Time { return now },
		IntervalTiers: []IntervalTier{
			{Within: 2 * time.Hour, Interval: 5 * time.Minute},
			{Within: 12 * time.Hour, Interval: 10 * time.Minute},
			{Within: 48 * time.Hour, Interval: 20 * time.Minute},
			{Within: 0, Interval: 30 * time.Minute},
		},
	}}

	cases := []struct {
		name string
		left time.Duration
		want time.Duration
	}{
		{"30分钟后截止", 30 * time.Minute, 5 * time.Minute},
		{"1.5小时后截止", 90 * time.Minute, 5 * time.Minute},
		{"6小时后截止", 6 * time.Hour, 10 * time.Minute},
		{"30小时后截止", 30 * time.Hour, 20 * time.Minute},
		{"5天后截止", 5 * 24 * time.Hour, 30 * time.Minute},
	}
	for _, c := range cases {
		task := []domain.Task{{ID: "t", Type: domain.TaskHomework, Deadline: now.Add(c.left)}}
		if got := s.nextWait(task); got != c.want {
			t.Errorf("%s：期望 %v，实际 %v", c.name, c.want, got)
		}
	}

	// 完全没有任务 → 走兜底档
	if got := s.nextWait(nil); got != 30*time.Minute {
		t.Errorf("无任务时应走兜底档 30m，实际 %v", got)
	}
}

// TestNextWaitTierNightOverride 验证夜间档位优先于分级表。
func TestNextWaitTierNightOverride(t *testing.T) {
	night := time.Date(2026, 11, 7, 3, 0, 0, 0, time.Local)
	s := &Scheduler{opts: Options{
		IdleInterval: 60 * time.Minute,
		ActiveStart:  7,
		ActiveEnd:    23,
		Clock:        func() time.Time { return night },
		IntervalTiers: []IntervalTier{
			{Within: 2 * time.Hour, Interval: 5 * time.Minute},
			{Within: 0, Interval: 30 * time.Minute},
		},
	}}
	// 即使 30 分钟后截止，夜间也应改为 60m（提醒由策略层的静默规则兜底）
	task := []domain.Task{{ID: "t", Type: domain.TaskHomework, Deadline: night.Add(30 * time.Minute)}}
	if got := s.nextWait(task); got != 60*time.Minute {
		t.Fatalf("夜间应覆盖分级表为 60m，实际 %v", got)
	}
}
