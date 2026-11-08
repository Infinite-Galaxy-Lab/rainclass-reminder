package domain

import (
	"testing"
	"time"
)

func ptrTime(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
	if err != nil {
		panic(err)
	}
	return t
}

// nothingSent 表示"从未发送过任何提醒"（空集合）。
// Decide 现在接收的是"已发送集合"，因此空 map 即"全部未发送"。
func nothingSent() map[string]bool { return map[string]bool{} }

// allSent 返回某任务全部里程碑均已发送的集合。
func allSent(task Task) map[string]bool {
	out := map[string]bool{}
	for _, ms := range MilestonesFor(task, DefaultMilestoneConfig()) {
		out[ms.Key()] = true
	}
	return out
}

func TestHumanDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30秒"},
		{45 * time.Minute, "45分钟"},
		{2 * time.Hour, "2小时"},
		{2*time.Hour + 30*time.Minute, "2小时30分钟"},
		{26 * time.Hour, "1天2小时"},
		{48 * time.Hour, "2天"},
	}
	for _, c := range cases {
		if got := HumanDuration(c.d); got != c.want {
			t.Errorf("HumanDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestMilestonesForHomework(t *testing.T) {
	dl := ptrTime("2026-11-10 12:00")
	task := Task{ID: "t1", Type: TaskHomework, Title: "作业1", Deadline: dl, Status: StatusPending}

	ms := MilestonesFor(task, DefaultMilestoneConfig())
	if len(ms) != 5 {
		t.Fatalf("期望 5 个里程碑（4 个窗口 + 1 个过期），实际 %d", len(ms))
	}
	if want := dl.Add(-24 * time.Hour); !ms[0].FireAt.Equal(want) {
		t.Errorf("首个里程碑时间 = %v, want %v", ms[0].FireAt, want)
	}
	if last := ms[len(ms)-1]; last.Kind != KindExpired {
		t.Errorf("最后里程碑类型 = %v, want expired", last.Kind)
	}
}

func TestMilestonesForClass(t *testing.T) {
	start := ptrTime("2026-11-08 09:00")
	task := Task{ID: "c1", Type: TaskClass, Title: "第3讲", StartAt: start}

	ms := MilestonesFor(task, DefaultMilestoneConfig())
	if len(ms) != 1 {
		t.Fatalf("课堂且无截止应只有 1 个开课提醒，实际 %d", len(ms))
	}
	if ms[0].Kind != KindClassStarting {
		t.Errorf("类型 = %v, want class_starting", ms[0].Kind)
	}
	if want := start.Add(-10 * time.Minute); !ms[0].FireAt.Equal(want) {
		t.Errorf("开课提醒时间 = %v, want %v", ms[0].FireAt, want)
	}
}

func TestMilestonesSkipDone(t *testing.T) {
	task := Task{ID: "d1", Type: TaskHomework, Title: "已完成", Deadline: ptrTime("2026-11-10 12:00"), Status: StatusDone}
	if ms := MilestonesFor(task, DefaultMilestoneConfig()); len(ms) != 0 {
		t.Errorf("已完成任务不应产生提醒，实际 %d", len(ms))
	}
}

func TestMilestoneKeyChangesWithDeadline(t *testing.T) {
	task := Task{ID: "t1", Type: TaskHomework, Title: "作业", Deadline: ptrTime("2026-11-10 12:00")}
	a := MilestonesFor(task, DefaultMilestoneConfig())[0]

	task.Deadline = ptrTime("2026-11-12 12:00")
	b := MilestonesFor(task, DefaultMilestoneConfig())[0]

	if a.Key() == b.Key() {
		t.Error("截止时间改变后去重键应不同（否则改期不会重新提醒）")
	}
}

func TestDecideCoalescePerTask(t *testing.T) {
	// 11:50 时，15 分钟（11:45）与 60/360/1440 分钟窗口均已到点。
	now := ptrTime("2026-11-10 11:50")
	dl := ptrTime("2026-11-10 12:00")
	task := Task{ID: "t1", Type: TaskHomework, Title: "作业", Deadline: dl, Status: StatusPending}

	got := Decide(now, []Task{task}, nothingSent(), DefaultPolicyConfig())
	sendable := Sendable(got)
	if len(sendable) != 1 {
		t.Fatalf("同一任务多窗口同时到点时应只发 1 条，实际 %d", len(sendable))
	}
	if sendable[0].Milestone.OffsetMinutes != 15 {
		t.Errorf("应选最紧急的 15 分钟窗口，实际 %d", sendable[0].Milestone.OffsetMinutes)
	}
}

func TestDecideQuietHours(t *testing.T) {
	now := ptrTime("2026-11-10 23:30")
	dl := ptrTime("2026-11-11 18:00") // 距截止 18.5 小时
	task := Task{ID: "t2", Type: TaskHomework, Title: "作业", Deadline: dl, Status: StatusPending}

	got := Decide(now, []Task{task}, nothingSent(), DefaultPolicyConfig())
	if len(got) == 0 {
		t.Fatal("应有顺延记录")
	}
	for _, r := range got {
		if r.Reason != "quiet_hours_deferred" {
			t.Errorf("静默时段内非紧急提醒应被顺延，实际 reason=%q", r.Reason)
		}
		// 23:30 已过当日 07:30，因此顺延点是次日 07:30。
		want := time.Date(now.Year(), now.Month(), now.Day(), 7, 30, 0, 0, now.Location()).Add(24 * time.Hour)
		if !r.FireAt.Equal(want) {
			t.Errorf("应顺延到次日 07:30，实际 %v", r.FireAt)
		}
	}
	if s := Sendable(got); len(s) != 0 {
		t.Errorf("被顺延的提醒本轮不应发送，实际 %d", len(s))
	}
}

func TestDecideQuietHoursUrgentPasses(t *testing.T) {
	now := ptrTime("2026-11-10 23:50")
	dl := ptrTime("2026-11-11 00:05") // 15 分钟内
	task := Task{ID: "t3", Type: TaskExam, Title: "考试", Deadline: dl, Status: StatusPending}

	got := Sendable(Decide(now, []Task{task}, nothingSent(), DefaultPolicyConfig()))
	if len(got) != 1 {
		t.Fatalf("紧急提醒应穿透静默时段，实际发送 %d 条", len(got))
	}
	if got[0].Milestone.OffsetMinutes != 15 {
		t.Errorf("应为 15 分钟窗口，实际 %d", got[0].Milestone.OffsetMinutes)
	}
}

func TestDecideSkipsAlreadySent(t *testing.T) {
	now := ptrTime("2026-11-10 11:50")
	dl := ptrTime("2026-11-10 12:00")
	task := Task{ID: "t4", Type: TaskHomework, Title: "作业", Deadline: dl, Status: StatusPending}

	if got := Sendable(Decide(now, []Task{task}, nothingSent(), DefaultPolicyConfig())); len(got) == 0 {
		t.Fatal("未发送过的提醒应被触发")
	}
	// 全部标记为已发送
	if again := Sendable(Decide(now, []Task{task}, allSent(task), DefaultPolicyConfig())); len(again) != 0 {
		t.Errorf("已发送的提醒不应重复，实际 %d", len(again))
	}
}

func TestDecideMaxLookahead(t *testing.T) {
	// 两个月前就已截止的任务：过期提醒不应在开机时补发。
	now := ptrTime("2026-01-01 00:00")
	dl := ptrTime("2025-11-01 00:00")
	task := Task{ID: "t5", Type: TaskHomework, Title: "陈旧任务", Deadline: dl, Status: StatusPending}

	if got := Sendable(Decide(now, []Task{task}, nothingSent(), DefaultPolicyConfig())); len(got) != 0 {
		t.Errorf("超出 lookahead 的陈旧提醒不应补发，实际 %d", len(got))
	}
}

func TestDecideSkipsFutureMilestones(t *testing.T) {
	now := ptrTime("2026-01-01 00:00")
	dl := ptrTime("2026-01-05 00:00") // 4 天后，尚未进入任何窗口
	task := Task{ID: "t6", Type: TaskHomework, Title: "未来任务", Deadline: dl, Status: StatusPending}

	if got := Sendable(Decide(now, []Task{task}, nothingSent(), DefaultPolicyConfig())); len(got) != 0 {
		t.Errorf("未到点的提醒不应发送，实际 %d", len(got))
	}
}
