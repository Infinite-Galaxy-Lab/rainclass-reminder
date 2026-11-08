package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/2963609262/ykt-reminder/internal/netguard"
)

// Feishu 是飞书自定义机器人通道。
//
// 安全措施：
//   - 发送前用 netguard 校验 webhook（仅 https + 白名单域名 + 非内网 IP）；
//   - 每次发送都重新校验，防止配置被篡改后指向内网；
//   - 支持签名校验（机器人开启"签名校验"时必需）。
type Feishu struct {
	webhook string
	secret  string
	atUser  string
	http    *http.Client
	guard   netguard.Policy
}

// NewFeishu 构造飞书通道；webhook 为空时返回 nil（视为未配置）。
func NewFeishu(webhook, secret, atUser string) (Notifier, error) {
	webhook = strings.TrimSpace(webhook)
	if webhook == "" {
		return nil, nil
	}

	guard := netguard.FeishuPolicy()
	if _, err := guard.ValidateURL(webhook); err != nil {
		return nil, fmt.Errorf("飞书 webhook 校验失败: %w", err)
	}

	return &Feishu{
		webhook: webhook,
		secret:  strings.TrimSpace(secret),
		atUser:  strings.TrimSpace(atUser),
		http: &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		guard: guard,
	}, nil
}

// Name 实现 Notifier。
func (f *Feishu) Name() string { return "feishu" }

// feishuPayload 是飞书 webhook 的请求体（interactive 卡片）。
type feishuPayload struct {
	Timestamp string            `json:"timestamp,omitempty"`
	Sign      string            `json:"sign,omitempty"`
	MsgType   string            `json:"msg_type"`
	Card      map[string]any    `json:"card,omitempty"`
	Content   map[string]string `json:"content,omitempty"`
}

// Send 实现 Notifier：发送一张富文本卡片。
func (f *Feishu) Send(ctx context.Context, msg Message) error {
	// 每次发送前重新校验，避免运行期配置被改写。
	if _, err := f.guard.ValidateURL(f.webhook); err != nil {
		return fmt.Errorf("飞书 webhook 不安全: %w", err)
	}

	payload := feishuPayload{
		MsgType: "interactive",
		Card:    f.buildCard(msg),
	}
	if f.secret != "" {
		ts, sign := feishuSign(f.secret, time.Now())
		payload.Timestamp = ts
		payload.Sign = sign
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.webhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := f.http.Do(req)
	if err != nil {
		return fmt.Errorf("飞书请求失败: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
	}()

	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("飞书返回 HTTP %d: %.200s", resp.StatusCode, string(b))
	}

	// 飞书即使 HTTP 200 也可能在 body 里报错：{"code":19021,"msg":"sign match fail"}
	var ar struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		// 新版返回
		StatusCode int    `json:"StatusCode"`
		StatusMsg  string `json:"StatusMessage"`
	}
	if err := json.Unmarshal(b, &ar); err == nil {
		if ar.Code != 0 {
			return fmt.Errorf("飞书拒绝: code=%d msg=%s", ar.Code, ar.Msg)
		}
		if ar.StatusCode != 0 {
			return fmt.Errorf("飞书拒绝: code=%d msg=%s", ar.StatusCode, ar.StatusMsg)
		}
	}
	return nil
}

// buildCard 构造飞书交互卡片。
func (f *Feishu) buildCard(msg Message) map[string]any {
	// 颜色：紧急=红，警告=橙，普通=蓝
	template := "blue"
	switch msg.Level {
	case "urgent":
		template = "red"
	case "warn":
		template = "orange"
	}

	elements := []map[string]any{
		{
			"tag":  "div",
			"text": map[string]any{"tag": "lark_md", "content": msg.Markdown},
		},
		{
			"tag": "note",
			"elements": []map[string]any{
				{"tag": "plain_text", "content": "长江雨课堂提醒 · " + Now().Format("01-02 15:04")},
			},
		},
	}

	if f.atUser != "" {
		elements = append(elements, map[string]any{
			"tag": "div",
			"text": map[string]any{
				"tag":     "lark_md",
				"content": fmt.Sprintf(`<at id="%s"></at>`, f.atUser),
			},
		})
	}

	return map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": template,
			"title":    map[string]any{"tag": "plain_text", "content": msg.Title},
		},
		"elements": elements,
	}
}

// feishuSign 计算飞书自定义机器人签名。
//
// 算法：以 "timestamp\nsecret" 为 key，对空串做 HmacSHA256，再 base64。
func feishuSign(secret string, ts time.Time) (string, string) {
	timestamp := fmt.Sprintf("%d", ts.Unix())
	stringToSign := timestamp + "\n" + secret

	mac := hmac.New(sha256.New, []byte(stringToSign))
	mac.Write([]byte{})
	return timestamp, base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
