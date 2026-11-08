package ykt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestFetchAllTouchesSession 验证每轮抓取都会触达 /v/course_meta/user_info。
//
// 为什么必须触达：这是全站唯一下发 Set-Cookie 的端点（实测 2026-11）。
// 不调用它，CookieObserver 永远收不到新 csrftoken，回写机制等于死代码。
// 写这个测试之前的实测里就出现过 refreshed_at 一直是零值的情况。
func TestFetchAllTouchesSession(t *testing.T) {
	var mu sync.Mutex
	var paths []string

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()

		switch {
		case strings.HasPrefix(r.URL.Path, "/v/course_meta/user_info"):
			http.SetCookie(w, &http.Cookie{Name: "csrftoken", Value: "fresh-csrf", Path: "/"})
			_, _ = w.Write([]byte(`{"msg":"0.0.1","data":{}}`))
		case strings.HasPrefix(r.URL.Path, "/v2/api/web/courses/list"):
			_, _ = w.Write([]byte(`{"errcode":0,"data":{"list":[]}}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
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

	if _, _, err := c.FetchAll(context.Background(), 7); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	var touched bool
	for _, p := range paths {
		if strings.HasPrefix(p, "/v/course_meta/user_info") {
			touched = true
			break
		}
	}
	if !touched {
		t.Errorf("FetchAll 未触达会话端点，实际请求路径: %v", paths)
	}
	if got := sess.observed["csrftoken"]; got != "fresh-csrf" {
		t.Errorf("应吸收新 csrftoken，实际 %q", got)
	}
}

// TestCourseMetaUserInfoAuthError 验证该端点也走统一的登录失效判定。
func TestCourseMetaUserInfoAuthError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL, MinRequestGap: 0, Transport: srv.Client().Transport})
	if err != nil {
		t.Fatal(err)
	}
	err = c.CourseMetaUserInfo(context.Background())
	if !IsAuthError(err) {
		t.Errorf("401 应判定为登录失效，实际 %v", err)
	}
}
