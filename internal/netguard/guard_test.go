package netguard

import (
	"errors"
	"net"
	"testing"
)

func TestValidateURLScheme(t *testing.T) {
	p := Policy{AllowHTTP: false}
	if _, err := p.ValidateURL("http://open.feishu.cn/x"); !errors.Is(err, ErrBlocked) {
		t.Errorf("未开启 AllowHTTP 时应拒绝 http，实际 err=%v", err)
	}
	p.AllowHTTP = true
	if _, err := p.ValidateURL("http://127.0.0.1/x"); !errors.Is(err, ErrBlocked) {
		t.Errorf("环回地址必须被拒绝，实际 err=%v", err)
	}
}

func TestValidateIPRejectsPrivate(t *testing.T) {
	p := Policy{}
	bad := []string{
		"127.0.0.1",
		"127.1.2.3",
		"::1",
		"10.0.0.1",
		"172.16.0.1",
		"192.168.1.1",
		"169.254.169.254", // 云元数据地址，必须拒绝
		"100.64.0.1",      // CGNAT
		"0.0.0.0",
		"224.0.0.1",
		"fc00::1",
		"::",
	}
	for _, s := range bad {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("测试用例 IP 非法: %s", s)
		}
		if err := p.ValidateIP(ip); !errors.Is(err, ErrBlocked) {
			t.Errorf("应拒绝 %s，实际 err=%v", s, err)
		}
	}
}

func TestValidateIPAllowsPublic(t *testing.T) {
	p := Policy{}
	good := []string{"1.1.1.1", "8.8.8.8", "2400:3200::1"}
	for _, s := range good {
		ip := net.ParseIP(s)
		if err := p.ValidateIP(ip); err != nil {
			t.Errorf("应允许公网地址 %s，实际 err=%v", s, err)
		}
	}
}

func TestHostWhitelist(t *testing.T) {
	p := Policy{AllowHosts: []string{"open.feishu.cn", "*.larksuite.com"}}
	if !p.hostAllowed("open.feishu.cn") {
		t.Error("精确匹配应通过")
	}
	if !p.hostAllowed("Open.Feishu.CN") {
		t.Error("匹配应忽略大小写")
	}
	if !p.hostAllowed("x.larksuite.com") {
		t.Error("通配后缀应通过")
	}
	if p.hostAllowed("evil.com") {
		t.Error("白名单外域名应被拒绝")
	}
	if p.hostAllowed("feishu.cn.evil.com") {
		t.Error("后缀伪装应被拒绝")
	}
}

func TestValidateURLFeishuPolicy(t *testing.T) {
	p := FeishuPolicy()
	if _, err := p.ValidateURL("https://open.feishu.cn/open-apis/bot/v2/hook/abc"); err != nil {
		t.Errorf("合法飞书地址应通过，实际 err=%v", err)
	}
	if _, err := p.ValidateURL("https://evil.example.com/hook"); !errors.Is(err, ErrBlocked) {
		t.Errorf("非飞书域名应被拒绝，实际 err=%v", err)
	}
	if _, err := p.ValidateURL("https://open.feishu.cn.evil.com/hook"); !errors.Is(err, ErrBlocked) {
		t.Errorf("域名后缀伪装应被拒绝，实际 err=%v", err)
	}
}
