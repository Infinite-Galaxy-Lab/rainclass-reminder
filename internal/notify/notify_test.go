package notify

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/2963609262/ykt-reminder/internal/domain"
)

func TestFeishuSignDeterministic(t *testing.T) {
	ts := time.Unix(1700000000, 0)
	tsStr, sign := feishuSign("secret123", ts)
	if tsStr != "1700000000" {
		t.Errorf("timestamp = %q", tsStr)
	}
	if sign == "" {
		t.Fatal("签名不应为空")
	}
	// 同一输入必须产生同一签名。
	_, sign2 := feishuSign("secret123", ts)
	if sign != sign2 {
		t.Error("相同输入应得到相同签名")
	}
	// 不同密钥应产生不同签名。
	_, sign3 := feishuSign("other", ts)
	if sign == sign3 {
		t.Error("不同密钥应产生不同签名")
	}
}

func TestNewFeishuEmptyWebhook(t *testing.T) {
	n, err := NewFeishu("", "", "")
	if err != nil {
		t.Fatalf("空 webhook 不应报错: %v", err)
	}
	if n != nil {
		t.Error("空 webhook 应返回 nil（视为未配置）")
	}
}

func TestNewFeishuRejectsUnsafeURL(t *testing.T) {
	cases := []string{
		"http://open.feishu.cn/open-apis/bot/v2/hook/x", // 非 https
		"https://127.0.0.1/hook",                        // 环回
		"https://192.168.1.10/hook",                     // 私有
		"https://evil.example.com/hook",                 // 非白名单
		"https://open.feishu.cn.evil.com/hook",          // 域名伪装
		"https://169.254.169.254/latest/meta-data/",     // 云元数据
	}
	for _, u := range cases {
		if _, err := NewFeishu(u, "", ""); err == nil {
			t.Errorf("不安全 URL 应被拒绝: %s", u)
		}
	}
}

func TestNewFeishuAcceptsOfficial(t *testing.T) {
	n, err := NewFeishu("https://open.feishu.cn/open-apis/bot/v2/hook/abcd-1234", "", "")
	if err != nil {
		t.Fatalf("官方地址应通过: %v", err)
	}
	if n == nil {
		t.Fatal("应返回通道实例")
	}
	if n.Name() != "feishu" {
		t.Errorf("Name() = %q", n.Name())
	}
}

func TestRenderReminderLevels(t *testing.T) {
	now := time.Date(2026, 11, 10, 11, 0, 0, 0, time.Local)
	clock := func() time.Time { return now }

	base := domain.Task{
		ID: "t1", Type: domain.TaskHomework, Title: "作业一", Course: "高数",
		Deadline: now.Add(30 * time.Minute), Status: domain.StatusPending,
	}

	cases := []struct {
		offset int
		kind   domain.MilestoneKind
		want   string
	}{
		{1440, domain.KindBeforeDeadline, "info"},
		{60, domain.KindBeforeDeadline, "warn"},
		{15, domain.KindBeforeDeadline, "urgent"},
		{0, domain.KindExpired, "urgent"},
	}

	for _, c := range cases {
		r := domain.Reminder{
			Task: base,
			Milestone: domain.Milestone{
				TaskID: "t1", Kind: c.kind, OffsetMinutes: c.offset,
				FireAt: now, Deadline: base.Deadline,
			},
		}
		msg := RenderReminder(r, clock)
		if msg.Level != c.want {
			t.Errorf("offset=%d kind=%s 级别 = %q, want %q", c.offset, c.kind, msg.Level, c.want)
		}
		if msg.Title == "" || msg.Markdown == "" {
			t.Errorf("offset=%d 标题与正文不应为空", c.offset)
		}
	}
}

func TestRenderReminderContainsKeyFacts(t *testing.T) {
	now := time.Date(2026, 11, 10, 11, 0, 0, 0, time.Local)
	task := domain.Task{
		ID: "t1", Type: domain.TaskExam, Title: "期中考试", Course: "线性代数",
		Deadline: now.Add(2 * time.Hour), Status: domain.StatusPending,
		URL: "https://changjiang.yuketang.cn/x",
	}
	msg := RenderReminder(domain.Reminder{
		Task:      task,
		Milestone: domain.Milestone{TaskID: "t1", Kind: domain.KindBeforeDeadline, OffsetMinutes: 60, Deadline: task.Deadline},
	}, func() time.Time { return now })

	for _, want := range []string{"期中考试", "线性代数", "考试", "2小时", "changjiang.yuketang.cn"} {
		if !contains(msg.Markdown, want) {
			t.Errorf("正文应包含 %q，实际:\n%s", want, msg.Markdown)
		}
	}
}

func TestRenderDigest(t *testing.T) {
	now := time.Date(2026, 11, 10, 11, 0, 0, 0, time.Local)
	rs := []domain.Reminder{
		{Task: domain.Task{ID: "a", Title: "作业A", Type: domain.TaskHomework, Deadline: now.Add(time.Hour)}},
		{Task: domain.Task{ID: "b", Title: "作业B", Type: domain.TaskExam, Deadline: now.Add(2 * time.Hour)}},
	}
	msg := RenderDigest(rs, func() time.Time { return now })
	if !contains(msg.Markdown, "作业A") || !contains(msg.Markdown, "作业B") {
		t.Errorf("汇总应包含所有任务:\n%s", msg.Markdown)
	}
	if RenderDigest(nil, func() time.Time { return now }).Markdown != "" {
		t.Error("空列表正文应为空")
	}
}

func TestRegistryUnknownChannel(t *testing.T) {
	reg, warns, err := NewRegistry([]string{"nonexistent"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reg.Empty() {
		t.Error("未知通道不应启用")
	}
	if len(warns) == 0 {
		t.Error("应提示未知通道")
	}
}

func TestRegistrySendFallback(t *testing.T) {
	// 第一个通道失败、第二个成功 → 整体应视为成功。
	fail := &stubNotifier{name: "fail", err: errStub}
	ok := &stubNotifier{name: "ok"}

	reg, _, err := NewRegistry([]string{"fail", "ok"}, map[string]func() (Notifier, error){
		"fail": func() (Notifier, error) { return fail, nil },
		"ok":   func() (Notifier, error) { return ok, nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	results, err := reg.Send(nil, Message{Title: "t", Markdown: "m"})
	if err != nil {
		t.Errorf("有一路成功即不应整体失败，实际: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("应返回 2 条结果，实际 %d", len(results))
	}
	if !ok.called {
		t.Error("第二通道应被调用")
	}
}

func TestRegistryAllFail(t *testing.T) {
	fail := &stubNotifier{name: "fail", err: errStub}
	reg, _, _ := NewRegistry([]string{"fail"}, map[string]func() (Notifier, error){
		"fail": func() (Notifier, error) { return fail, nil },
	})
	if _, err := reg.Send(nil, Message{}); err == nil {
		t.Error("全部通道失败时应返回错误")
	}
}

type stubNotifier struct {
	name   string
	err    error
	called bool
}

func (s *stubNotifier) Name() string { return s.name }
func (s *stubNotifier) Send(_ context.Context, _ Message) error {
	s.called = true
	return s.err
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

var errStub = errors.New("stub failure")
