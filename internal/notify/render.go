package notify

import (
	"fmt"
	"strings"
	"time"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/domain"
)

// 本文件负责"领域对象 → 通知文案"的渲染。
// 渲染与通道解耦：同一份 Markdown 既能给飞书，也能给邮件/QQ。
// 文案规则集中在 render.go，避免每个通道各写一套。

// RenderReminder 把一条提醒渲染成通知消息。
func RenderReminder(r domain.Reminder, now timeNow) Message {
	t := r.Task
	at := now()
	title, level := headline(r)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("**%s**\n", t.Title))

	if t.Course != "" {
		b.WriteString(fmt.Sprintf("课程：%s\n", t.Course))
	}
	b.WriteString(fmt.Sprintf("类型：%s\n", t.Type.Label()))

	if t.HasDeadline() {
		b.WriteString(fmt.Sprintf("截止：%s\n", t.Deadline.In(at.Location()).Format("01-02 15:04")))
		b.WriteString(fmt.Sprintf("剩余：**%s**\n", t.Remaining(at)))
	}
	if !t.StartAt.IsZero() {
		b.WriteString(fmt.Sprintf("开始：%s\n", t.StartAt.In(at.Location()).Format("01-02 15:04")))
	}
	if t.URL != "" {
		b.WriteString(fmt.Sprintf("\n[打开雨课堂](%s)", t.URL))
	}

	return Message{
		Title:    title,
		Markdown: b.String(),
		Urgent:   r.Milestone.Kind == domain.KindExpired || r.Milestone.OffsetMinutes <= 15,
		Level:    level,
	}
}

// headline 生成标题与级别。
func headline(r domain.Reminder) (title string, level string) {
	t := r.Task
	switch {
	case r.Milestone.Kind == domain.KindExpired:
		return fmt.Sprintf("⏰ 已截止：%s %s", t.Type.Label(), t.Title), "urgent"
	case r.Milestone.Kind == domain.KindClassStarting:
		return fmt.Sprintf("🔔 %s 即将开始：%s", t.Type.Label(), t.Title), "warn"
	case r.Milestone.OffsetMinutes <= 15:
		return fmt.Sprintf("🚨 %s 即将截止：%s", t.Type.Label(), t.Title), "urgent"
	case r.Milestone.OffsetMinutes <= 60:
		return fmt.Sprintf("⚠️ %s 一小时内截止：%s", t.Type.Label(), t.Title), "warn"
	default:
		return fmt.Sprintf("📌 %s 提醒：%s", t.Type.Label(), t.Title), "info"
	}
}

// RenderDigest 把多条提醒渲染成一条汇总（用于降噪后的合并通知）。
func RenderDigest(reminders []domain.Reminder, now timeNow) Message {
	if len(reminders) == 0 {
		return Message{Title: "暂无提醒", Markdown: ""}
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("共 **%d** 项任务需要关注：\n\n", len(reminders)))
	for i, r := range reminders {
		t := r.Task
		line := fmt.Sprintf("%d. **%s**（%s）", i+1, t.Title, t.Type.Label())
		if t.HasDeadline() {
			line += fmt.Sprintf(" — 剩余 %s", t.Remaining(now()))
		}
		b.WriteString(line + "\n")
	}
	return Message{
		Title:    fmt.Sprintf("📋 %d 项任务提醒", len(reminders)),
		Markdown: b.String(),
		Level:    "warn",
	}
}

// RenderAuthExpired 渲染"登录失效"告警。
func RenderAuthExpired(webUIAddr string) Message {
	md := "雨课堂登录态已失效，**截止提醒已暂停**。\n\n"
	if webUIAddr != "" {
		md += fmt.Sprintf("请打开本地面板重新扫码：http://%s\n\n", webUIAddr)
	}
	md += "或在终端执行：`rainclass-reminder auth login`"
	return Message{
		Title:    "🔑 需要重新登录雨课堂",
		Markdown: md,
		Urgent:   true,
		Level:    "urgent",
	}
}

// timeNow 是时间源签名，便于测试注入。
type timeNow = func() time.Time
