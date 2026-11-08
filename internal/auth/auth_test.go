package auth

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestParseCookieStringFull(t *testing.T) {
	c, err := ParseCookieString("sessionid=abc123; csrftoken=xyz789")
	if err != nil {
		t.Fatal(err)
	}
	if c.SessionID != "abc123" {
		t.Errorf("sessionid = %q, want abc123", c.SessionID)
	}
	if c.CSRF != "xyz789" {
		t.Errorf("csrftoken = %q, want xyz789", c.CSRF)
	}
	if !c.Valid() {
		t.Error("应视为有效登录态")
	}
}

func TestParseCookieStringOnlySessionID(t *testing.T) {
	c, err := ParseCookieString("abc123")
	if err != nil {
		t.Fatal(err)
	}
	if c.SessionID != "abc123" {
		t.Errorf("裸值应作为 sessionid，实际 %q", c.SessionID)
	}
}

func TestParseCookieStringQuoted(t *testing.T) {
	c, err := ParseCookieString(`"sessionid=abc; csrftoken=def"`)
	if err != nil {
		t.Fatal(err)
	}
	if c.SessionID != "abc" || c.CSRF != "def" {
		t.Errorf("引号包裹应被去除: %+v", c)
	}
}

func TestParseCookieStringEmpty(t *testing.T) {
	if _, err := ParseCookieString("   "); err == nil {
		t.Error("空内容应报错")
	}
}

func TestParseCookieStringNoSession(t *testing.T) {
	if _, err := ParseCookieString("foo=bar"); err == nil {
		t.Error("缺少 sessionid 时应报错")
	}
}

func TestCookieApply(t *testing.T) {
	c := Cookie{SessionID: "s1", CSRF: "c1"}
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	c.Apply(req)

	if got := req.Header.Get("Cookie"); got != "sessionid=s1; csrftoken=c1" {
		t.Errorf("Cookie 头 = %q", got)
	}
	if got := req.Header.Get("X-CSRFToken"); got != "c1" {
		t.Errorf("X-CSRFToken = %q", got)
	}
}

func TestCookieRedacted(t *testing.T) {
	c := Cookie{SessionID: "abcdefghijklmn"}
	got := c.Redacted()
	if got == "sessionid=abcdefghijklmn" {
		t.Error("脱敏后不应包含完整 sessionid")
	}
	if got == "" {
		t.Error("脱敏输出不应为空")
	}
}

func TestStoreSaveLoadClear(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cookie.json")

	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Valid() {
		t.Error("初始状态应为未登录")
	}

	want := Cookie{SessionID: "sess", CSRF: "csrf", UserHint: "张三"}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}

	// 重新打开，验证持久化。
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.Current()
	if got.SessionID != "sess" || got.CSRF != "csrf" || got.UserHint != "张三" {
		t.Errorf("持久化内容不符: %+v", got)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("应记录更新时间")
	}

	// 权限检查（Unix 下应为 0600；Windows 不强制）。
	if fi, err := os.Stat(path); err == nil {
		if mode := fi.Mode().Perm(); mode&0o077 != 0 {
			t.Logf("提示：文件权限为 %o，建议 0600", mode)
		}
	}

	if err := s2.Clear(); err != nil {
		t.Fatal(err)
	}
	if s2.Valid() {
		t.Error("清除后应无效")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("清除应删除文件")
	}
}

func TestStoreCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cookie.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(path); err == nil {
		t.Error("损坏的 cookie 文件应报错提示重新导入")
	}
}
