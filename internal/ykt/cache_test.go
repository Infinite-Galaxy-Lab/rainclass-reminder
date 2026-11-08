package ykt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestLeafInfoCached 验证详情接口在 TTL 内只请求一次。
//
// 这是控制请求量的核心：实测单轮会拉 279 次详情，
// 若每次都真打接口，10 分钟一轮就是每小时 1674 次，很容易被风控。
func TestLeafInfoCached(t *testing.T) {
	var hits int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"errcode":0,"data":{"name":"第三章习题","score_deadline":1794585599000}}`))
	}))
	defer srv.Close()

	c, err := New(Options{
		BaseURL:       srv.URL,
		ChapterTTL:    time.Minute,
		DetailTTL:     time.Minute,
		MinRequestGap: 0, // 测试里不需要节流
		Transport:     srv.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		info, err := c.LeafInfo(ctx, "1001", "2002")
		if err != nil {
			t.Fatalf("第 %d 次调用失败: %v", i+1, err)
		}
		if info.Title.String() != "第三章习题" {
			t.Fatalf("标题解析错误: %q", info.Title.String())
		}
	}

	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("TTL 内应只请求 1 次，实际 %d 次", got)
	}
}

// TestLeafInfoCacheExpires 验证 TTL 过期后会重新请求。
func TestLeafInfoCacheExpires(t *testing.T) {
	var hits int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(`{"success":true,"errcode":0,"data":{"title":"x"}}`))
	}))
	defer srv.Close()

	// TTL 设得极小，保证必然过期。
	c, err := New(Options{
		BaseURL:       srv.URL,
		DetailTTL:     time.Nanosecond,
		MinRequestGap: 0,
		Transport:     srv.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if _, err := c.LeafInfo(ctx, "1", "2"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := c.LeafInfo(ctx, "1", "2"); err != nil {
		t.Fatal(err)
	}

	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("TTL 过期后应重新请求，期望 2 次实际 %d 次", got)
	}
}

// TestPaceEnforcesGap 验证全局限速确实拉开请求间隔。
func TestPaceEnforcesGap(t *testing.T) {
	var hits int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(`{"success":true,"errcode":0,"data":{}}`))
	}))
	defer srv.Close()

	const gap = 60 * time.Millisecond
	c, err := New(Options{
		BaseURL:       srv.URL,
		MinRequestGap: gap,
		Transport:     srv.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	start := time.Now()
	for i := 0; i < 4; i++ {
		if err := c.getJSON(ctx, "/x", nil, &struct{}{}); err != nil {
			t.Fatalf("请求失败: %v", err)
		}
	}
	elapsed := time.Since(start)

	// 4 次请求、间隔 60ms → 至少要 3 个间隔 = 180ms。
	if elapsed < 3*gap {
		t.Fatalf("限速未生效：耗时 %v，应至少 %v", elapsed, 3*gap)
	}
}
