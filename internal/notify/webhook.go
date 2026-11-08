package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Webhook 是一个"通用 HTTP 推送"通道。
//
// 用途：飞书自定义机器人被企业禁用时，可以用它对接任何接受
// HTTP POST 的推送服务：
//
//	Bark         https://api.day.app/{key}            （iOS，body: {"title","body"}）
//	Server酱      https://sctapi.ftqq.com/{key}.send    （微信，body: title/desp 表单）
//	PushPlus     http://www.pushplus.plus/send        （微信，JSON body）
//	企业微信群机器人 https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...
//	钉钉群机器人   https://oapi.dingtalk.com/robot/send?access_token=...
//	Discord      https://discord.com/api/webhooks/{id}/{token}
//	Telegram     https://api.telegram.org/bot{token}/sendMessage
//
// 设计取舍：不为每个服务写适配器（配置面太碎），而是提供两种
// 通用载荷格式，覆盖绝大多数服务：
//
//	json  → {"title":"...","body":"...","content":"...","text":"..."}
//	text  → 纯文本正文（适合 Telegram 的 text 字段或 webhook 直收）
//
// 安全：目标 URL 每次发送前都用 netguard 校验（仅 https、拒绝内网），
// 且默认要求显式开启（enabled 列表里写 webhook 才算启用）。
type Webhook struct {
	name    string
	target  string
	format  string // json / text
	extra   map[string]string
	headers map[string]string
	http    *http.Client
	guard   guardPolicy
	// tmplTitle / tmplBody 允许自定义字段名（少数服务需要）。
	titleField string
	bodyField  string
}

// guardPolicy 是 netguard.Policy 的最小接口，便于测试替换。
type guardPolicy interface {
	ValidateURL(raw string) (*url.URL, error)
}

// WebhookOptions 是构造通用通道的参数。
type WebhookOptions struct {
	// Name 是通道标识（日志与配置里显示）。
	Name string
	// URL 是目标地址，必须 https。
	URL string
	// Format 取 "json" 或 "text"，默认 json。
	Format string
	// Extra 是附加到 JSON 载荷顶层或 text 载荷查询串的键值对，
	// 用于满足某些服务的必填字段（如企业微信的 msgtype）。
	Extra map[string]string
	// TitleField / BodyField 覆盖默认字段名（默认 title / body）。
	TitleField string
	BodyField  string
	// Headers 是附加请求头，如 {"Authorization": "Bearer xxx"}。
	//
	// 之前 config 里定义了这个字段但没往下传，用户配了会被静默忽略，
	// 表现为"配了 token 还是 401"，很难排查。
	Headers map[string]string
	// Guard 是 SSRF 校验策略；为 nil 时使用默认策略（拒绝内网）。
	Guard guardPolicy
	// Client 允许测试注入自定义 http.Client（如信任测试服务器证书）。
	Client *http.Client
}

// NewWebhook 构造通用 HTTP 推送通道。
func NewWebhook(opts WebhookOptions) (Notifier, error) {
	target := strings.TrimSpace(opts.URL)
	if target == "" {
		return nil, nil // 未配置视为未启用
	}
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = "webhook"
	}
	format := strings.ToLower(strings.TrimSpace(opts.Format))
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "text" {
		return nil, fmt.Errorf("webhook 格式只支持 json / text，实际 %q", format)
	}

	guard := opts.Guard
	if guard == nil {
		guard = defaultWebhookGuard()
	}
	u, err := guard.ValidateURL(target)
	if err != nil {
		return nil, fmt.Errorf("webhook 地址校验失败: %w", err)
	}
	// 只允许 https：明文 http 会让凭据与任务标题在网络上裸奔。
	if u.Scheme != "https" {
		return nil, fmt.Errorf("webhook 必须使用 https（当前 %q）", u.Scheme)
	}

	titleField := strings.TrimSpace(opts.TitleField)
	if titleField == "" {
		titleField = "title"
	}
	bodyField := strings.TrimSpace(opts.BodyField)
	if bodyField == "" {
		bodyField = "body"
	}

	return &Webhook{
		name:       name,
		target:     target,
		format:     format,
		extra:      opts.Extra,
		titleField: titleField,
		bodyField:  bodyField,
		headers:    opts.Headers,
		guard:      guard,
		http:       clientOrDefault(opts.Client),
	}, nil
}

// clientOrDefault 返回注入的 client，或构造默认 client。
func clientOrDefault(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Name 实现 Notifier。
func (w *Webhook) Name() string { return w.name }

// Send 实现 Notifier。
func (w *Webhook) Send(ctx context.Context, msg Message) error {
	// 每次发送前重新校验，防止运行期配置被改写成内网地址。
	if _, err := w.guard.ValidateURL(w.target); err != nil {
		return fmt.Errorf("webhook 地址不安全: %w", err)
	}

	body := plainText(msg.Markdown)
	if body == "" {
		body = msg.Title
	}

	var payload []byte
	var contentType string

	switch w.format {
	case "text":
		// 纯文本：标题 + 正文，适合直接展示。
		payload = []byte(msg.Title + "\n\n" + body)
		contentType = "text/plain; charset=utf-8"
	default:
		m := map[string]string{}
		for k, v := range w.extra {
			m[k] = v
		}
		m[w.titleField] = msg.Title
		m[w.bodyField] = body
		// 常见别名，提升"通用"命中率：content / text / msg 一并带上。
		if _, ok := m["content"]; !ok {
			m["content"] = msg.Title + "\n\n" + body
		}
		if _, ok := m["text"]; !ok {
			m["text"] = msg.Title + "\n\n" + body
		}
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		payload = b
		contentType = "application/json; charset=utf-8"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.target, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "ykt-reminder/"+Version)
	// 自定义头放在最后，允许覆盖上面的默认值（例如某些服务要求特定 UA）。
	for k, v := range w.headers {
		if k = strings.TrimSpace(k); k != "" {
			req.Header.Set(k, v)
		}
	}

	resp, err := w.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s 请求失败: %w", w.name, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s 返回 HTTP %d", w.name, resp.StatusCode)
	}
	return nil
}

// Version 由外部在构建时注入（避免 import 循环，这里用变量约定）。
var Version = "dev"
