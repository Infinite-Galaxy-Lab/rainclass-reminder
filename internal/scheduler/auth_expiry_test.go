package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/domain"
	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/ykt"
)

func TestCycleAuthFailurePausesRemindersAndAlertsOnce(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	tasks := []domain.Task{{ID: "synthetic", Type: domain.TaskHomework, Deadline: now.Add(5 * time.Minute)}}
	s, notifier, repo := setupScheduler(t, tasks, now)
	if err := repo.SaveTasks(context.Background(), tasks); err != nil {
		t.Fatal(err)
	}
	source := s.opts.Source.(*fakeSource)
	source.err = ykt.ErrAuthExpired
	for i := 0; i < 2; i++ {
		result := s.RunCycle(context.Background())
		if result.Err == nil || result.Sent != 0 || len(result.Tasks) != 1 {
			t.Fatalf("expired cycle: %+v", result)
		}
	}
	if len(notifier.sent) != 1 || notifier.sent[0].Title != "🔑 需要重新登录雨课堂" {
		t.Fatalf("auth alerts: %+v", notifier.sent)
	}
	source.err = ykt.ErrUpstream
	source.tasks = nil
	if err := repo.SaveTasks(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	s.RunCycle(context.Background())
	if len(notifier.sent) != 1 {
		t.Fatal("network failure sent auth alert")
	}
	source.err = ykt.ErrAuthExpired
	s.RunCycle(context.Background())
	if len(notifier.sent) != 1 {
		t.Fatal("network failure cleared auth deduplication")
	}
	source.err = nil
	s.RunCycle(context.Background())
	source.err = ykt.ErrAuthExpired
	s.RunCycle(context.Background())
	if len(notifier.sent) != 2 {
		t.Fatal("successful refresh did not reset auth alert")
	}
}
