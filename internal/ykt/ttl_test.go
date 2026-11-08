package ykt

import (
	"testing"
	"time"
)

// TestTTLForTiers 验证详情缓存 TTL 按"距截止还剩多久"分档。
//
// 这是压降请求量的关键：远期任务的截止时间几乎不会变，
// 用 8 小时的 TTL 而不是统一的 60 分钟，能少打大量重复请求。
func TestTTLForTiers(t *testing.T) {
	c := &Client{
		detailTTL: 60 * time.Minute,
		detailTTLTiers: []CacheTier{
			{Within: 2 * time.Hour, TTL: 15 * time.Minute},
			{Within: 24 * time.Hour, TTL: 60 * time.Minute},
			{Within: 0, TTL: 8 * time.Hour},
		},
	}

	cases := []struct {
		name     string
		deadline time.Time
		want     time.Duration
	}{
		{"30分钟后截止", time.Now().Add(30 * time.Minute), 15 * time.Minute},
		{"1小时后截止", time.Now().Add(time.Hour), 15 * time.Minute},
		{"6小时后截止", time.Now().Add(6 * time.Hour), 60 * time.Minute},
		{"20小时后截止", time.Now().Add(20 * time.Hour), 60 * time.Minute},
		{"3天后截止", time.Now().Add(72 * time.Hour), 8 * time.Hour},
		{"85天后截止", time.Now().Add(85 * 24 * time.Hour), 8 * time.Hour},
		{"已过期", time.Now().Add(-time.Hour), 15 * time.Minute},
		{"未知截止", time.Time{}, 15 * time.Minute},
	}
	for _, c2 := range cases {
		if got := c.ttlFor(c2.deadline); got != c2.want {
			t.Errorf("%s：期望 %v，实际 %v", c2.name, c2.want, got)
		}
	}
}

// TestTTLForNoTiers 没有分级表时退回统一的 DetailTTL。
func TestTTLForNoTiers(t *testing.T) {
	c := &Client{detailTTL: 42 * time.Minute}
	got := c.ttlFor(time.Now().Add(100 * time.Hour))
	if got != 42*time.Minute {
		t.Fatalf("无分级表时应用 DetailTTL，实际 %v", got)
	}
}
