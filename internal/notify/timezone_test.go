package notify

import (
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/domain"
)

func TestReminderRendersClockTimeZone(t *testing.T) {
	at := time.Date(2026, 11, 10, 16, 0, 0, 0, time.UTC)
	reminder := domain.Reminder{Task: domain.Task{
		ID: "synthetic", Type: domain.TaskHomework, Deadline: at.Add(2 * time.Hour), StartAt: at.Add(time.Hour),
	}}
	for _, tc := range []struct{ zone, deadline, start string }{
		{"Asia/Shanghai", "截止：11-11 02:00", "开始：11-11 01:00"},
		{"America/New_York", "截止：11-10 13:00", "开始：11-10 12:00"},
	} {
		zone, err := time.LoadLocation(tc.zone)
		if err != nil {
			t.Fatal(err)
		}
		message := RenderReminder(reminder, func() time.Time { return at.In(zone) })
		if !strings.Contains(message.Markdown, tc.deadline) || !strings.Contains(message.Markdown, tc.start) {
			t.Fatalf("%s: %s", tc.zone, message.Markdown)
		}
	}
}
