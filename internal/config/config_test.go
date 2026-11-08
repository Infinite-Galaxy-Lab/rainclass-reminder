package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultValues(t *testing.T) {
	cfg := Default()
	if cfg.Poll.Interval != 10*time.Minute {
		t.Errorf("默认轮询间隔 = %v", cfg.Poll.Interval)
	}
	if cfg.Ykt.BaseURL != "https://changjiang.yuketang.cn" {
		t.Errorf("默认站点 = %q", cfg.Ykt.BaseURL)
	}
	if len(cfg.Policy.MilestonesBeforeMinutes) != 4 {
		t.Errorf("默认里程碑数 = %d", len(cfg.Policy.MilestonesBeforeMinutes))
	}
	if !cfg.WebUI.Enabled || cfg.WebUI.Listen != "127.0.0.1:8787" {
		t.Errorf("默认面板配置不符: %+v", cfg.WebUI)
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
poll:
  interval: 5m
  concurrency: 8
ykt:
  base_url: https://example.yuketang.cn
notify:
  enabled: [feishu]
  feishu:
    webhook: https://open.feishu.cn/open-apis/bot/v2/hook/abc
policy:
  milestones_before_minutes: [720, 120, 30]
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Poll.Interval != 5*time.Minute {
		t.Errorf("interval = %v", cfg.Poll.Interval)
	}
	if cfg.Poll.Concurrency != 8 {
		t.Errorf("concurrency = %d", cfg.Poll.Concurrency)
	}
	if cfg.Ykt.BaseURL != "https://example.yuketang.cn" {
		t.Errorf("base_url = %q", cfg.Ykt.BaseURL)
	}
	if !cfg.FeishuReady() {
		t.Error("应识别飞书已配置")
	}
	if len(cfg.Policy.MilestonesBeforeMinutes) != 3 {
		t.Errorf("里程碑覆盖失败: %v", cfg.Policy.MilestonesBeforeMinutes)
	}
	// 未在文件里出现的字段应保留默认值。
	if cfg.WebUI.Listen != "127.0.0.1:8787" {
		t.Errorf("未覆盖字段应保留默认值，实际 %q", cfg.WebUI.Listen)
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("文件不存在不应报错: %v", err)
	}
	if cfg.Poll.Interval != 10*time.Minute {
		t.Error("应使用默认值")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("poll: [unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("非法 YAML 应报错")
	}
}

func TestValidateRejectsHTTPBaseURL(t *testing.T) {
	cfg := Default()
	cfg.Ykt.BaseURL = "http://changjiang.yuketang.cn"
	if err := cfg.Validate(); err == nil {
		t.Error("非 https 站点应被拒绝")
	}
}

func TestValidateRejectsTooShortInterval(t *testing.T) {
	cfg := Default()
	cfg.Poll.Interval = 10 * time.Second
	if err := cfg.Validate(); err == nil {
		t.Error("过短的轮询间隔应被拒绝（易触发限流）")
	}
}

func TestValidateClampsConcurrency(t *testing.T) {
	cfg := Default()
	cfg.Poll.Concurrency = 100
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Poll.Concurrency > 16 {
		t.Errorf("并发应被限制在 16 以内，实际 %d", cfg.Poll.Concurrency)
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("YKT_FEISHU_WEBHOOK", "https://open.feishu.cn/open-apis/bot/v2/hook/env")
	t.Setenv("YKT_FEISHU_SECRET", "envsecret")
	t.Setenv("YKT_DATA_DIR", filepath.Join(t.TempDir(), "data"))

	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Notify.Feishu.Webhook != "https://open.feishu.cn/open-apis/bot/v2/hook/env" {
		t.Errorf("环境变量未生效: %q", cfg.Notify.Feishu.Webhook)
	}
	if cfg.Notify.Feishu.Secret != "envsecret" {
		t.Errorf("secret 环境变量未生效")
	}
}

func TestRedacted(t *testing.T) {
	cfg := Default()
	cfg.Notify.Feishu.Webhook = "https://open.feishu.cn/open-apis/bot/v2/hook/secret-token"
	r := cfg.Redacted()

	feishu, ok := r["feishu"].(string)
	if !ok {
		t.Fatal("缺少 feishu 键")
	}
	if feishu == cfg.Notify.Feishu.Webhook {
		t.Error("脱敏输出不应包含完整 webhook")
	}
	if !containsStr(feishu, "***") {
		t.Errorf("脱敏应包含掩码，实际 %q", feishu)
	}
}

func TestSessionCookieFromEnv(t *testing.T) {
	t.Setenv("YKT_SESSION_COOKIE", "sessionid=abc; csrftoken=def")
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionCookie() != "sessionid=abc; csrftoken=def" {
		t.Errorf("环境变量 cookie 未生效: %q", cfg.SessionCookie())
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
