package ykt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSession 是最小的 Session + CookieObserver 实现。
type fakeSession struct {
	mu        sync.Mutex
	sessionID string
	csrf      string
	observed  map[string]string
	// applied 记录每次 Apply 写出的 Cookie 头，用于检查是否重复。
	applied []string
}

func (f *fakeSession) Apply(req *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h := "sessionid=" + f.sessionID + "; csrftoken=" + f.csrf
	f.applied = append(f.applied, h)
	req.Header.Set("Cookie", h)
}

func (f *fakeSession) Valid() bool { return true }

func (f *fakeSession) ObserveCookie(name, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.observed == nil {
		f.observed = make(map[string]string)
	}
	f.observed[name] = value
	if name == "csrftoken" {
		f.csrf = value
	}
	return nil
}

func (f *fakeSession) cookieHeaders() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.applied...)
}

// TestObservePicksUpSetCookie 验证响应里的 Set-Cookie 会交给 Session。
func TestObservePicksUpSetCookie(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "csrftoken", Value: "fresh", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "django_language", Value: "zh-cn", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"errcode":0,"data":{"title":"t"}}`))
	}))
	defer srv.Close()

	sess := &fakeSession{sessionID: "s1", csrf: "stale"}
	c, err := New(Options{
		BaseURL:       srv.URL,
		MinRequestGap: 0,
		Session:       sess,
		Transport:     srv.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if _, err := c.LeafInfo(ctx, "1001", "2002"); err != nil {
		t.Fatal(err)
	}

	// 一次请求只有一条路径：不应该出现重复的 Cookie 头设置。
	hdrs := sess.cookieHeaders()
	if len(hdrs) != 1 {
		t.Fatalf("应只有 1 次 Apply，实际 %d 次: %v", len(hdrs), hdrs)
	}
	if strings.Count(hdrs[0], "csrftoken=") != 1 {
		t.Errorf("Cookie 头里 csrftoken 重复: %q", hdrs[0])
	}

	if got := sess.observed["csrftoken"]; got != "fresh" {
		t.Errorf("应捕获新 csrftoken，实际 %q", got)
	}
	if got := sess.observed["django_language"]; got != "zh-cn" {
		t.Errorf("应捕获辅助 cookie，实际 %q", got)
	}
}

// TestObserveOnErrorStatus 验证 4xx/5xx 响应上的 Set-Cookie 同样被吸收。
//
// 服务端在拒绝请求时也可能顺手刷新 csrftoken，丢掉就白丢一次续期。
func TestObserveOnErrorStatus(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "csrftoken", Value: "rotated", Path: "/"})
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	sess := &fakeSession{sessionID: "s1", csrf: "stale"}
	c, err := New(Options{
		BaseURL:       srv.URL,
		MinRequestGap: 0,
		Session:       sess,
		Transport:     srv.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.LeafInfo(context.Background(), "1", "2"); err == nil {
		t.Fatal("5xx 应返回错误")
	}
	if got := sess.observed["csrftoken"]; got != "rotated" {
		t.Errorf("错误响应上的 Set-Cookie 也应被吸收，实际 %q", got)
	}
}

// TestNoCookieJarOnAPIRequests 是 bug #20 的回归测试。
//
// 若 API 请求的 client 挂了 cookie jar，net/http 会把 jar 里的 cookie
// 追加到 Session 手写的 Cookie 头之后，同一个 cookie 名出现两次。
//
// 注意：必须用不同的 leafID，否则命中详情缓存、第二个请求根本不会发出，
// 测试就会假通过（写这个测试时踩过一次）。
func TestNoCookieJarOnAPIRequests(t *testing.T) {
	var mu sync.Mutex
	var gotCookie []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotCookie = append(gotCookie, r.Header.Get("Cookie"))
		mu.Unlock()
		// 服务端下发 sessionid，验证它不会在下一个请求里被 jar 重复带上。
		http.SetCookie(w, &http.Cookie{Name: "sessionid", Value: "from-server", Path: "/"})
		_, _ = w.Write([]byte(`{"success":true,"errcode":0,"data":{"title":"t"}}`))
	}))
	defer srv.Close()

	sess := &staleSession{}
	c, err := New(Options{
		BaseURL:       srv.URL,
		MinRequestGap: 0,
		Session:       sess,
		Transport:     srv.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	for _, leaf := range []string{"2002", "2003", "2004"} {
		if _, err := c.LeafInfo(ctx, "1001", leaf); err != nil {
			t.Fatal(err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(gotCookie) != 3 {
		t.Fatalf("应发出 3 个真实请求，实际 %d 个", len(gotCookie))
	}
	for i, h := range gotCookie {
		if n := strings.Count(h, "sessionid="); n != 1 {
			t.Errorf("第 %d 个请求的 Cookie 头里 sessionid 出现 %d 次: %q", i+1, n, h)
		}
	}
}

// staleSession 只实现 Session（不实现 CookieObserver），用固定 cookie。
type staleSession struct{}

func (staleSession) Apply(req *http.Request) {
	req.Header.Set("Cookie", "sessionid=mine; csrftoken=c")
}
func (staleSession) Valid() bool { return true }

// TestSessionWithoutObserver 验证 Session 没实现 CookieObserver 时不 panic。
func TestSessionWithoutObserver(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "x", Value: "1", Path: "/"})
		_, _ = w.Write([]byte(`{"success":true,"errcode":0,"data":{"title":"t"}}`))
	}))
	defer srv.Close()

	c, err := New(Options{
		BaseURL:       srv.URL,
		MinRequestGap: 0,
		Session:       staleSession{},
		Transport:     srv.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.LeafInfo(context.Background(), "1", "2"); err != nil {
		t.Fatalf("不应因缺少观察者而失败: %v", err)
	}
}

// TestAuthHTTPClientHasJar 验证登录流程用的 client 带 jar、业务 client 不带。
func TestAuthHTTPClientHasJar(t *testing.T) {
	c, err := New(Options{BaseURL: "https://example.com", MinRequestGap: 0})
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPClient().Jar == nil {
		t.Error("登录流程 client 必须带 cookie jar")
	}
	if c.http.Jar != nil {
		t.Error("业务 client 不能带 cookie jar（会与 Session 手写的 Cookie 头叠加）")
	}
	if c.HTTPClient() == c.http {
		t.Error("登录流程与业务请求必须是两个不同的 client")
	}
}

// TestTTLForUnknownDeadline 验证未知截止时间用最短 TTL，尽快补全。
func TestTTLForUnknownDeadline(t *testing.T) {
	c, err := New(Options{
		BaseURL:       "https://example.com",
		MinRequestGap: 0,
		DetailTTL:     time.Hour,
		DetailTTLTiers: []CacheTier{
			{Within: 2 * time.Hour, TTL: 15 * time.Minute},
			{Within: 0, TTL: 8 * time.Hour},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.ttlFor(time.Time{}); got != 15*time.Minute {
		t.Errorf("未知截止应用最短档 15m，实际 %s", got)
	}
}
