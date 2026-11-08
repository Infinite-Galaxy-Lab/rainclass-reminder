package auth

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestObserveCookieUpdatesCSRF 验证服务端下发的新 csrftoken 会被记下。
//
// 实测 /v/course_meta/user_info 每次响应都会带新的 csrftoken；
// 记不住就等于一直拿导入时的旧值去发 X-CSRFToken 头。
func TestObserveCookieUpdatesCSRF(t *testing.T) {
	s, err := NewStore(filepath.Join(t.TempDir(), "cookie.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(Cookie{SessionID: "sess", CSRF: "old"}); err != nil {
		t.Fatal(err)
	}

	if err := s.ObserveCookie("csrftoken", "new"); err != nil {
		t.Fatal(err)
	}

	got := s.Current()
	if got.CSRF != "new" {
		t.Errorf("csrftoken 未更新，实际 %q", got.CSRF)
	}
	if got.RefreshedAt.IsZero() {
		t.Error("应记录回写时间")
	}
	if got.SessionID != "sess" {
		t.Errorf("sessionid 不应被影响，实际 %q", got.SessionID)
	}
	// 导入时间不能因为回写而变化，否则 UI 上"这次登录是什么时候"就失真了。
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt 应保留导入时间")
	}
}

// TestObserveCookieRefreshedAtAdvancesWithoutChange 验证
// "值没变但服务端下发了 cookie" 也要推进 RefreshedAt。
//
// 背景：实测 csrftoken 在同一会话内连打 3 次完全一致。若只在值变化时
// 更新时间戳，面板上的"续期"会永远冻在登录那一刻，看起来像很久没动，
// 反而误导用户以为会话已经僵死。
func TestObserveCookieRefreshedAtAdvancesWithoutChange(t *testing.T) {
	s, err := NewStore(filepath.Join(t.TempDir(), "cookie.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(Cookie{SessionID: "sess", CSRF: "same"}); err != nil {
		t.Fatal(err)
	}
	s.minWriteGap = time.Hour // 只验证内存语义

	if err := s.ObserveCookie("csrftoken", "same"); err != nil {
		t.Fatal(err)
	}

	got := s.Current()
	if got.RefreshedAt.IsZero() {
		t.Error("值未变化时也应推进 RefreshedAt（服务端确认了会话有效）")
	}
	if got.CSRF != "same" {
		t.Errorf("csrftoken 不应被改动，实际 %q", got.CSRF)
	}

	// 值没变时不该立即写盘。注意不能用"文件里有没有 refreshed_at 这个键"
	// 来判断——time.Time 是结构体，json 的 omitempty 对结构体无效，
	// 零值也会被序列化成 "0001-01-01T00:00:00Z"。要看值。
	if got := readRefreshedAt(t, s.Path()); !got.IsZero() {
		t.Errorf("值未变化时不应立即写盘，磁盘上 refreshed_at = %s", got)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := readRefreshedAt(t, s.Path()); got.IsZero() {
		t.Error("Flush 后应把时间戳落盘")
	}
}

// readRefreshedAt 从磁盘上的 cookie.json 读出 refreshed_at。
func readRefreshedAt(t *testing.T, path string) time.Time {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var c Cookie
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c.RefreshedAt
}

// TestObserveCookieNoLoginIgnores 验证未登录时不会凭空造出登录态。
//
// 危险场景：cookie 已被清除，但某个响应仍带 csrftoken。
// 若照收不误，会写出一个"看着有 csrftoken 但没有 sessionid"的半残文件。
func TestObserveCookieNoLoginIgnores(t *testing.T) {
	s, err := NewStore(filepath.Join(t.TempDir(), "cookie.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ObserveCookie("csrftoken", "x"); err != nil {
		t.Fatal(err)
	}
	if s.Valid() {
		t.Error("未登录时不应接受回写")
	}
	if got := s.Current(); got.CSRF != "" {
		t.Errorf("不应写入字段，实际 csrftoken=%q", got.CSRF)
	}
}

// TestObserveCookieExtra 验证非 sessionid/csrftoken 的 cookie 落入 Extra。
func TestObserveCookieExtra(t *testing.T) {
	s, err := NewStore(filepath.Join(t.TempDir(), "cookie.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(Cookie{SessionID: "sess"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ObserveCookie("django_language", "zh-cn"); err != nil {
		t.Fatal(err)
	}
	got := s.Current()
	if got.Extra["django_language"] != "zh-cn" {
		t.Errorf("Extra 未记录，实际 %+v", got.Extra)
	}
	if names := got.Names(); len(names) != 2 {
		t.Errorf("Names 应包含 sessionid 与 django_language，实际 %v", names)
	}
}

// TestObserveCookieThrottled 验证回写落盘有节流，但内存值始终是最新的。
//
// 回写在每个响应上都会触发，而一轮抓取有上百个响应；
// 不节流就是上百次同步写盘，会明显拖慢抓取。
func TestObserveCookieThrottled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cookie.json")
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(Cookie{SessionID: "sess", CSRF: "v0"}); err != nil {
		t.Fatal(err)
	}
	// 把节流窗口设得足够长，确保第二次回写一定被压住。
	s.minWriteGap = time.Hour

	if err := s.ObserveCookie("csrftoken", "v1"); err != nil {
		t.Fatal(err)
	}
	// 内存值必须立刻可见（请求用的是内存值）。
	if got := s.Current().CSRF; got != "v1" {
		t.Errorf("内存值应已更新，实际 %q", got)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"csrftoken": "v1"`) {
		t.Error("节流窗口内不应写盘")
	}

	// Flush 应该把滞后的内容补上。
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"csrftoken": "v1"`) {
		t.Error("Flush 后应已落盘")
	}
}

// TestObserveCookiePersisted 验证回写内容能跨进程重载。
func TestObserveCookiePersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookie.json")
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(Cookie{SessionID: "sess", CSRF: "old"}); err != nil {
		t.Fatal(err)
	}
	s.minWriteGap = 0 // 关掉节流，验证立即落盘
	if err := s.ObserveCookie("csrftoken", "new"); err != nil {
		t.Fatal(err)
	}

	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Current().CSRF; got != "new" {
		t.Errorf("重载后 csrftoken 应为 new，实际 %q", got)
	}
}

// TestObserveCookieConcurrent 用 -race 验证并发出回写不会撕裂状态。
func TestObserveCookieConcurrent(t *testing.T) {
	s, err := NewStore(filepath.Join(t.TempDir(), "cookie.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(Cookie{SessionID: "sess"}); err != nil {
		t.Fatal(err)
	}
	s.minWriteGap = time.Hour // 只测内存并发，避免测试里反复写盘

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = s.ObserveCookie("k"+string(rune('a'+i)), "v")
				_ = s.Current()
			}
		}(i)
	}
	wg.Wait()

	if len(s.Current().Extra) != 8 {
		t.Errorf("应记录 8 个 Extra 键，实际 %d", len(s.Current().Extra))
	}
}

// TestCookieHeaderNoDuplicate 验证 Cookie 头里没有重复的 cookie 名。
//
// 这是 bug #20 的回归测试：早期给 API 请求也挂了 cookie jar，
// net/http 会把 jar 里的 cookie 追加到 Session 手写的 Cookie 头后面，
// 产生 "csrftoken=新; csrftoken=旧" 这类重复，服务端取哪个看实现。
func TestCookieHeaderNoDuplicate(t *testing.T) {
	c := Cookie{
		SessionID: "s",
		CSRF:      "c-new",
		Extra:     map[string]string{"csrftoken": "c-old", "sessionid": "s-old", "u": "1"},
	}
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	c.Apply(req)

	h := req.Header.Get("Cookie")
	for _, name := range []string{"sessionid", "csrftoken"} {
		if n := strings.Count(h, name+"="); n != 1 {
			t.Errorf("%s 应只出现一次，实际 %d 次：%q", name, n, h)
		}
	}
	if !strings.Contains(h, "csrftoken=c-new") {
		t.Errorf("专用字段应优先于 Extra：%q", h)
	}
	if !strings.Contains(h, "u=1") {
		t.Errorf("其余 cookie 不应丢失：%q", h)
	}
}

// TestCookieHeaderStableOrder 验证 Cookie 头顺序稳定，便于抓包比对。
func TestCookieHeaderStableOrder(t *testing.T) {
	c := Cookie{
		SessionID: "s",
		CSRF:      "c",
		Extra:     map[string]string{"b": "2", "a": "1", "d": "4", "cc": "3"},
	}
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	c.Apply(req)

	want := "sessionid=s; csrftoken=c; a=1; b=2; cc=3; d=4"
	if got := req.Header.Get("Cookie"); got != want {
		t.Errorf("Cookie 头 = %q，期望 %q", got, want)
	}
}

// TestParseCookieStringKeepsExtra 验证导入时不会丢掉辅助 cookie。
func TestParseCookieStringKeepsExtra(t *testing.T) {
	c, err := ParseCookieString("sessionid=s1; csrftoken=c1; university_id=77; django_language=zh-cn")
	if err != nil {
		t.Fatal(err)
	}
	if c.SessionID != "s1" || c.CSRF != "c1" {
		t.Errorf("主字段解析错误: %+v", c)
	}
	if c.Extra["university_id"] != "77" || c.Extra["django_language"] != "zh-cn" {
		t.Errorf("辅助 cookie 丢失: %+v", c.Extra)
	}
}

// TestParseCookieJSONObject 验证 {"k":"v"} 形式的导入。
func TestParseCookieJSONObject(t *testing.T) {
	c, err := ParseCookieString(`{"sessionid":"s1","csrftoken":"c1","university_id":"77"}`)
	if err != nil {
		t.Fatal(err)
	}
	if c.SessionID != "s1" || c.CSRF != "c1" || c.Extra["university_id"] != "77" {
		t.Errorf("JSON 对象解析错误: %+v", c)
	}
}

// TestParseCookieJSONArray 验证 DevTools 导出的数组形式。
func TestParseCookieJSONArray(t *testing.T) {
	raw := `[{"name":"sessionid","value":"s1"},{"name":"csrftoken","value":"c1"},{"name":"university_id","value":"77"}]`
	c, err := ParseCookieString(raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.SessionID != "s1" || c.CSRF != "c1" || c.Extra["university_id"] != "77" {
		t.Errorf("JSON 数组解析错误: %+v", c)
	}
}

// TestLegacyRawCookieMigrated 验证旧版 cookie.json（只有 raw）能平滑升级。
func TestLegacyRawCookieMigrated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookie.json")
	legacy := `{"raw":"sessionid=s-old; csrftoken=c-old; university_id=77","updated_at":"2026-11-01T10:00:00+08:00"}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got := s.Current()
	if got.SessionID != "s-old" || got.CSRF != "c-old" {
		t.Errorf("旧文件迁移失败: %+v", got)
	}
	if got.Extra["university_id"] != "77" {
		t.Errorf("旧文件里的辅助 cookie 未迁移: %+v", got.Extra)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("应保留原导入时间")
	}
}
