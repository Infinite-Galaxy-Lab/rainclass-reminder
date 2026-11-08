package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWebhookRejectsNonHTTPS 验证明文地址被拒绝。
func TestWebhookRejectsNonHTTPS(t *testing.T) {
	_, err := NewWebhook(WebhookOptions{URL: "http://example.com/hook"})
	if err == nil {
		t.Error("http 地址应被拒绝（凭据会明文传输）")
	}
}

// TestWebhookRejectsPrivateAddress 验证 SSRF 防护。
func TestWebhookRejectsPrivateAddress(t *testing.T) {
	cases := []string{
		"https://127.0.0.1/hook",
		"https://10.0.0.1/hook",
		"https://192.168.1.1/hook",
		"https://169.254.169.254/latest/meta-data/",
		"https://[::1]/hook",
	}
	for _, u := range cases {
		if _, err := NewWebhook(WebhookOptions{URL: u}); err == nil {
			t.Errorf("内网地址应被拒绝: %s", u)
		}
	}
}

// TestWebhookEmptyURL 验证未配置时返回 nil（视为未启用）。
func TestWebhookEmptyURL(t *testing.T) {
	n, err := NewWebhook(WebhookOptions{URL: ""})
	if err != nil {
		t.Fatalf("空 URL 不应报错: %v", err)
	}
	if n != nil {
		t.Error("空 URL 应返回 nil")
	}
}

// TestWebhookInvalidFormat 验证格式校验。
func TestWebhookInvalidFormat(t *testing.T) {
	if _, err := NewWebhook(WebhookOptions{
		URL:    "https://example.com/hook",
		Format: "xml",
	}); err == nil {
		t.Error("不支持的格式应报错")
	}
}

// TestWebhookJSONPayload 验证 JSON 载荷包含通用字段别名。
func TestWebhookJSONPayload(t *testing.T) {
	var got map[string]string
	var contentType string

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// 测试服务器用自签证书且监听 127.0.0.1，因此需要放宽 guard。
	n, err := NewWebhook(WebhookOptions{
		URL:    srv.URL + "/hook",
		Format: "json",
		Guard:  permissiveGuard(),
		Client: srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	msg := Message{Title: "标题", Markdown: "**正文**内容"}
	if err := n.Send(context.Background(), msg); err != nil {
		t.Fatalf("发送失败: %v", err)
	}

	if !strings.Contains(contentType, "application/json") {
		t.Errorf("Content-Type = %q", contentType)
	}
	if got["title"] != "标题" {
		t.Errorf("title = %q", got["title"])
	}
	if !strings.Contains(got["body"], "正文") {
		t.Errorf("body 应包含正文，实际 %q", got["body"])
	}
	// 通用别名应一并带上，提升对各种服务的命中率。
	for _, k := range []string{"content", "text"} {
		if got[k] == "" {
			t.Errorf("应包含通用字段 %q", k)
		}
	}
}

// TestWebhookTextFormat 验证纯文本格式。
func TestWebhookTextFormat(t *testing.T) {
	var got string
	var contentType string

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		got = string(buf[:n])
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n, err := NewWebhook(WebhookOptions{
		URL:    srv.URL + "/hook",
		Format: "text",
		Guard:  permissiveGuard(),
		Client: srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Send(context.Background(), Message{Title: "标题", Markdown: "正文"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(contentType, "text/plain") {
		t.Errorf("Content-Type = %q", contentType)
	}
	if !strings.Contains(got, "标题") || !strings.Contains(got, "正文") {
		t.Errorf("纯文本应包含标题与正文，实际 %q", got)
	}
}

// TestWebhookCustomFields 验证自定义字段名。
func TestWebhookCustomFields(t *testing.T) {
	var got map[string]string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n, _ := NewWebhook(WebhookOptions{
		URL:        srv.URL + "/hook",
		TitleField: "subject",
		BodyField:  "desp",
		Guard:      permissiveGuard(),
		Client:     srv.Client(),
	})
	if err := n.Send(context.Background(), Message{Title: "T", Markdown: "B"}); err != nil {
		t.Fatal(err)
	}
	if got["subject"] != "T" {
		t.Errorf("subject = %q", got["subject"])
	}
	if got["desp"] == "" {
		t.Error("desp 不应为空")
	}
}

// TestWebhookHTTPError 验证非 2xx 视为失败。
func TestWebhookHTTPError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n, _ := NewWebhook(WebhookOptions{URL: srv.URL + "/hook", Guard: permissiveGuard(), Client: srv.Client()})
	if err := n.Send(context.Background(), Message{Title: "T"}); err == nil {
		t.Error("HTTP 500 应返回错误")
	}
}

// TestPlainTextStripsMarkdown 验证纯文本转换。
func TestPlainTextStripsMarkdown(t *testing.T) {
	md := "**加粗**\n[链接](https://example.com)\n| 表格 | 行 |\n普通文字"
	got := plainText(md)

	if strings.Contains(got, "**") {
		t.Errorf("应去掉强调标记: %q", got)
	}
	if strings.Contains(got, "](") {
		t.Errorf("应去掉链接语法: %q", got)
	}
	if strings.Contains(got, "|") {
		t.Errorf("应去掉表格行: %q", got)
	}
	if !strings.Contains(got, "加粗") || !strings.Contains(got, "链接") || !strings.Contains(got, "普通文字") {
		t.Errorf("正文内容应保留: %q", got)
	}
}

// TestPlainTextTruncates 验证超长内容被截断（通知气泡高度有限）。
func TestPlainTextTruncates(t *testing.T) {
	md := strings.Repeat("一行内容\n", 20)
	got := plainText(md)
	lines := strings.Split(got, "\n")
	if len(lines) > 6 {
		t.Errorf("应截断到约 5 行，实际 %d 行", len(lines))
	}
}
