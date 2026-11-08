package notify

import (
	"context"
	"fmt"
	"strings"

	"github.com/gen2brain/beeep"
)

// Desktop 是桌面系统通知通道。
//
// 无需任何第三方服务与凭据，适合本地常驻场景：程序在哪台机器上跑，
// 就在哪台机器上弹通知。
//
// 实现说明：使用 beeep 库（Windows 走 WinRT Toast，macOS 走 osascript，
// Linux 走 D-Bus/notify-send）。本包**不自行拼装或执行 shell 命令**，
// 通知内容作为普通字符串参数传入，因此不存在命令注入面。
type Desktop struct {
	// AppName 显示在通知标题前，默认 "雨课堂提醒"。
	AppName string
}

// NewDesktop 构造桌面通知通道。
func NewDesktop(appName string) Notifier {
	if strings.TrimSpace(appName) == "" {
		appName = "雨课堂提醒"
	}
	// beeep 用全局变量决定通知来源显示名（Windows 通知卡片的左上角）。
	// 不设置会显示 "DefaultAppName"。
	beeep.AppName = appName
	return &Desktop{AppName: appName}
}

// Name 实现 Notifier。
func (d *Desktop) Name() string { return "desktop" }

// Send 实现 Notifier：调用平台原生通知。
func (d *Desktop) Send(ctx context.Context, msg Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// 桌面通知没有富文本能力，把 Markdown 粗略转成纯文本。
	body := plainText(msg.Markdown)
	if body == "" {
		body = msg.Title
	}

	// 标题不再拼 AppName：通知卡片本身已显示来源（beeep.AppName），
	// 重复拼接会出现"雨课堂提醒 · 雨课堂提醒 · ..."。
	if err := beeep.Notify(msg.Title, body, ""); err != nil {
		return fmt.Errorf("桌面通知发送失败: %w", err)
	}
	return nil
}

// plainText 把 Markdown 粗略转为纯文本（去链接语法、强调标记、表格）。
//
// 桌面通知通常只显示两三行，因此这里还会截断过长内容，
// 避免把整条卡片正文塞进通知气泡。
func plainText(md string) string {
	var b strings.Builder
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "|") || strings.HasPrefix(t, "---") {
			continue
		}
		// [文字](url) → 文字
		for {
			open := strings.Index(t, "[")
			if open < 0 {
				break
			}
			mid := strings.Index(t[open:], "](")
			if mid < 0 {
				break
			}
			mid += open
			end := strings.Index(t[mid:], ")")
			if end < 0 {
				break
			}
			end += mid
			t = t[:open] + t[open+1:mid] + t[end+1:]
		}
		t = strings.ReplaceAll(t, "**", "")
		t = strings.ReplaceAll(t, "__", "")
		t = strings.TrimPrefix(t, "- ")
		if t != "" {
			b.WriteString(t)
			b.WriteString("\n")
		}
	}

	out := strings.TrimSpace(b.String())
	// 通知气泡高度有限：最多保留 5 行。
	lines := strings.Split(out, "\n")
	if len(lines) > 5 {
		out = strings.Join(lines[:5], "\n") + "\n…"
	}
	return out
}
