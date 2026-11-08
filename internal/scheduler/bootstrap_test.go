package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/2963609262/ykt-reminder/internal/domain"
)

// TestBootstrapSilencesHistory 验证首次运行不会补发历史提醒。
//
// 这是真实使用中暴露的问题：新装用户第一次启动时，数据库为空，
// 所有历史任务的过期里程碑都会被判定为"该发未发"，
// 一次性补发数百条"已截止"通知。
func TestBootstrapSilencesHistory(t *testing.T) {
	now := time.Date(2026, 11, 7, 22, 0, 0, 0, time.Local)

	// 一个 200 天前就截止的历史任务 + 一个 2 小时后截止的任务。
	tasks := []domain.Task{
		{
			ID: "old", Type: domain.TaskHomework, Title: "旧作业",
			Deadline: now.Add(-200 * 24 * time.Hour), Status: domain.StatusPending,
		},
		{
			ID: "soon", Type: domain.TaskHomework, Title: "马上到期",
			Deadline: now.Add(2 * time.Hour), Status: domain.StatusPending,
		},
	}

	s, fn, _ := setupScheduler(t, tasks, now)

	res := s.RunCycle(context.Background())
	if !res.Bootstrapped {
		t.Error("首次运行应标记为 bootstrap")
	}
	// 历史任务的 5 个里程碑（4 窗口 + 过期）应被静默。
	// 2 小时后到期的任务：15 分钟窗口未来，但 60 分钟窗口已过点（1 小时前）
	// 且在 24 小时宽限内，因此可能仍会提醒。
	if len(fn.sent) > 1 {
		t.Errorf("不应补发多条历史提醒，实际发送 %d 条", len(fn.sent))
	}

	// 关键断言：绝不能出现"旧作业"的过期提醒。
	for _, msg := range fn.sent {
		if msg.Markdown != "" && containsStr(msg.Title, "旧作业") {
			t.Errorf("历史任务不应触发提醒: %s", msg.Title)
		}
	}
}

// TestBootstrapKeepsRecent 验证宽限窗口内的提醒仍会发出。
func TestBootstrapKeepsRecent(t *testing.T) {
	now := time.Date(2026, 11, 7, 22, 0, 0, 0, time.Local)

	// 3 小时前刚截止的任务：其"过期"提醒在 24 小时宽限内，应保留。
	tasks := []domain.Task{{
		ID: "recent", Type: domain.TaskHomework, Title: "刚截止的作业",
		Deadline: now.Add(-3 * time.Hour), Status: domain.StatusPending,
	}}

	s, fn, _ := setupScheduler(t, tasks, now)
	s.RunCycle(context.Background())

	if len(fn.sent) == 0 {
		t.Error("宽限窗口内刚截止的任务应提醒一次")
	}
}

// TestSecondRunNoResend 验证第二次运行不会重复提醒。
func TestSecondRunNoResend(t *testing.T) {
	now := time.Date(2026, 11, 7, 22, 0, 0, 0, time.Local)
	tasks := []domain.Task{{
		ID: "t1", Type: domain.TaskHomework, Title: "作业",
		Deadline: now.Add(10 * time.Minute), Status: domain.StatusPending,
	}}

	s, fn, _ := setupScheduler(t, tasks, now)

	first := s.RunCycle(context.Background())
	if first.Sent == 0 {
		t.Fatal("首轮应有提醒")
	}
	n := len(fn.sent)

	second := s.RunCycle(context.Background())
	if second.Sent != 0 {
		t.Errorf("第二轮不应重复提醒，实际 %d 条", second.Sent)
	}
	if len(fn.sent) != n {
		t.Errorf("通道总发送数不应变化")
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
