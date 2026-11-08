package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/infinite-galaxy/rainclass-reminder/internal/domain"
	"github.com/infinite-galaxy/rainclass-reminder/internal/notify"
	"github.com/infinite-galaxy/rainclass-reminder/internal/store"
)

// fakeSource 是可控的假数据源。
type fakeSource struct {
	tasks []domain.Task
	err   error
}

func (f *fakeSource) FetchTasks(context.Context) ([]domain.Task, error) {
	return f.tasks, f.err
}

// fakeNotifier 记录发送历史。
type fakeNotifier struct {
	sent []notify.Message
	fail bool
}

func (f *fakeNotifier) Name() string { return "fake" }

func (f *fakeNotifier) Send(_ context.Context, msg notify.Message) error {
	if f.fail {
		return context.DeadlineExceeded
	}
	f.sent = append(f.sent, msg)
	return nil
}

func setupScheduler(t *testing.T, tasks []domain.Task, now time.Time) (*Scheduler, *fakeNotifier, store.Repository) {
	t.Helper()
	repo, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	fn := &fakeNotifier{}
	reg, _, err := notify.NewRegistry([]string{"fake"}, map[string]func() (notify.Notifier, error){
		"fake": func() (notify.Notifier, error) { return fn, nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	policy := domain.DefaultPolicyConfig()
	s, err := New(Options{
		Interval:   time.Minute,
		Policy:     policy,
		Store:      repo,
		Notify:     reg,
		Source:     &fakeSource{tasks: tasks},
		Clock:      fakeClock(now),
		DetailDays: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, fn, repo
}

func TestRunCycleSendsOnce(t *testing.T) {
	// 11:50 时，只有 15 分钟窗口（11:45）已到点；60 分钟窗口（11:00）也已到点，
	// 因此 Coalesce 会只发一条（最紧急的 15 分钟）。
	now := time.Date(2026, 11, 10, 11, 50, 0, 0, time.Local)
	tasks := []domain.Task{
		{
			ID: "t1", Type: domain.TaskHomework, Title: "第三章作业",
			Course: "高等数学", Deadline: time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local),
			Status: domain.StatusPending, URL: "https://example.invalid/x",
		},
	}

	s, fn, _ := setupScheduler(t, tasks, now)

	res := s.RunCycle(context.Background())
	if res.Err != nil {
		t.Fatalf("本轮不应出错: %v", res.Err)
	}
	if res.Sent != 1 {
		t.Fatalf("应发送 1 条，实际 %d", res.Sent)
	}
	if len(fn.sent) != 1 {
		t.Fatalf("假通道应收到 1 条，实际 %d", len(fn.sent))
	}

	// 第二轮（同一时刻）：该任务的最紧急窗口已发，其余窗口被 coalesce 抑制，
	// 因此不应再次发送。
	res2 := s.RunCycle(context.Background())
	if res2.Sent != 0 {
		t.Errorf("第二轮不应重复发送，实际 %d", res2.Sent)
	}
	if len(fn.sent) != 1 {
		t.Errorf("通道总发送数应仍为 1，实际 %d", len(fn.sent))
	}
}

func TestRunCycleFailedSendRetriesNextCycle(t *testing.T) {
	now := time.Date(2026, 11, 10, 11, 50, 0, 0, time.Local)
	tasks := []domain.Task{{
		ID: "t1", Type: domain.TaskExam, Title: "期中考试",
		Course: "线性代数", Deadline: time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local),
		Status: domain.StatusPending,
	}}

	s, fn, _ := setupScheduler(t, tasks, now)
	fn.fail = true

	res := s.RunCycle(context.Background())
	if res.Failed != 1 {
		t.Fatalf("发送失败应计为 1，实际 %d", res.Failed)
	}
	if res.Sent != 0 {
		t.Errorf("失败时不应计为已发送，实际 %d", res.Sent)
	}

	// 恢复后下一轮应重试成功（因为失败时 Release 了占位）。
	fn.fail = false
	res2 := s.RunCycle(context.Background())
	// 注意：SetupScheduler 的 Clock 固定在同一时刻，失败时释放了 15 分钟窗口的占位，
	// 但 60/360/1440 分钟窗口也被 coalesce 压制且从未占位，因此重试时会再次选中
	// 最紧急的 15 分钟窗口。
	if res2.Sent != 1 {
		t.Errorf("恢复后应重试成功，实际发送 %d（failed=%d）", res2.Sent, res2.Failed)
	}
}

func TestRunCycleAuthAlertOnce(t *testing.T) {
	s, fn, _ := setupScheduler(t, nil, time.Now())

	ctx := context.Background()
	if err := s.AlertAuthExpired(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.AlertAuthExpired(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fn.sent) != 1 {
		t.Fatalf("登录失效告警只应发一次，实际 %d", len(fn.sent))
	}

	// 恢复登录后清除标记，下次失效应能再次告警。
	s.ClearAuthExpired(ctx)
	if err := s.AlertAuthExpired(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fn.sent) != 2 {
		t.Errorf("清除标记后应允许再次告警，实际 %d", len(fn.sent))
	}
}

func TestInActiveHours(t *testing.T) {
	cases := []struct {
		hour       int
		start, end int
		want       bool
	}{
		{9, 7, 23, true},
		{23, 7, 23, false},
		{2, 7, 23, false},
		{2, 23, 7, true}, // 跨午夜
		{9, 23, 7, false},
		{12, 0, 0, true}, // 关闭时段限制
	}
	for _, c := range cases {
		tm := time.Date(2026, 1, 1, c.hour, 0, 0, 0, time.Local)
		if got := inActiveHours(tm, c.start, c.end); got != c.want {
			t.Errorf("inActiveHours(%d, %d, %d) = %v, want %v", c.hour, c.start, c.end, got, c.want)
		}
	}
}
