// Package netguard 提供 SSRF 防护：在发起出站请求前校验目标地址。
//
// 设计目标：任何"用户可配置的 URL"（如飞书 webhook）都必须先过这里，
// 防止把本机/内网端点当作推送目标，也防止 DNS 重绑定绕过。
package netguard

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ErrBlocked 表示目标地址被策略拒绝。
var ErrBlocked = errors.New("目标地址不被允许")

// Policy 描述允许的出站目标。
type Policy struct {
	// AllowHosts 非空时，仅允许这些域名（忽略大小写，精确匹配或 *. 后缀匹配）。
	AllowHosts []string
	// AllowPrivate 仅用于本地调试；生产必须为 false。
	AllowPrivate bool
	// AllowHTTP 是否允许明文 http（本地调试用）。
	AllowHTTP bool
}

// ValidateURL 校验 URL 的 scheme、host 与解析出的 IP。
//
// 注意：这里做的是"解析后逐一校验"，因此仍可能被 TTL=0 的 DNS 重绑定绕过；
// 生产环境建议同时固定 AllowHosts 白名单（见 feishu 配置）。
func (p Policy) ValidateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: URL 解析失败: %v", ErrBlocked, err)
	}
	if u.Scheme != "https" && !(p.AllowHTTP && u.Scheme == "http") {
		return nil, fmt.Errorf("%w: 仅允许 https（当前 %q）", ErrBlocked, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("%w: 缺少 host", ErrBlocked)
	}
	if !p.hostAllowed(host) {
		return nil, fmt.Errorf("%w: host %q 不在白名单内", ErrBlocked, host)
	}
	if err := p.ValidateHost(host); err != nil {
		return nil, err
	}
	return u, nil
}

// hostAllowed 做大小写无关的白名单匹配，支持 "*.example.com"。
func (p Policy) hostAllowed(host string) bool {
	if len(p.AllowHosts) == 0 {
		return true
	}
	h := strings.ToLower(host)
	for _, a := range p.AllowHosts {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		if strings.HasPrefix(a, "*.") {
			suffix := a[1:] // ".example.com"
			if strings.HasSuffix(h, suffix) {
				return true
			}
			continue
		}
		if h == a {
			return true
		}
	}
	return false
}

// ValidateHost 解析域名的所有 A/AAAA 记录并逐一校验 IP。
func (p Policy) ValidateHost(host string) error {
	// 直接是 IP 字面量时无需解析。
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return p.ValidateIP(ip)
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("%w: 域名 %q 解析失败: %v", ErrBlocked, host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("%w: 域名 %q 无解析结果", ErrBlocked, host)
	}
	for _, ip := range ips {
		if err := p.ValidateIP(ip); err != nil {
			return err
		}
	}
	return nil
}

// ValidateIP 拒绝环回、私有、链路本地、组播、未指定与保留地址。
func (p Policy) ValidateIP(ip net.IP) error {
	if ip == nil {
		return fmt.Errorf("%w: 空 IP", ErrBlocked)
	}
	if p.AllowPrivate {
		return nil
	}
	if ip.IsLoopback() {
		return fmt.Errorf("%w: 环回地址 %s", ErrBlocked, ip)
	}
	if ip.IsPrivate() {
		return fmt.Errorf("%w: 私有地址 %s", ErrBlocked, ip)
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return fmt.Errorf("%w: 链路本地地址 %s", ErrBlocked, ip)
	}
	if ip.IsMulticast() {
		return fmt.Errorf("%w: 组播地址 %s", ErrBlocked, ip)
	}
	if ip.IsUnspecified() {
		return fmt.Errorf("%w: 未指定地址 %s", ErrBlocked, ip)
	}
	if isCGNAT(ip) {
		return fmt.Errorf("%w: CGNAT 地址 %s", ErrBlocked, ip)
	}
	if isReserved(ip) {
		return fmt.Errorf("%w: 保留地址 %s", ErrBlocked, ip)
	}
	return nil
}

// isCGNAT 判断 100.64.0.0/10（运营商级 NAT，属私有可路由段）。
func isCGNAT(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	return v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}

// isReserved 判断若干保留/文档/基准测试网段。
func isReserved(ip net.IP) bool {
	// 0.0.0.0/8、192.0.2.0/24（TEST-NET-1）、198.51.100.0/24（TEST-NET-2）、
	// 203.0.113.0/24（TEST-NET-3）、240.0.0.0/4（保留）
	if v4 := ip.To4(); v4 != nil {
		switch {
		case v4[0] == 0:
			return true
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 2:
			return true
		case v4[0] == 198 && v4[1] == 51 && v4[2] == 100:
			return true
		case v4[0] == 203 && v4[1] == 0 && v4[2] == 113:
			return true
		case v4[0] >= 240:
			return true
		}
		return false
	}
	// IPv6 唯一本地地址 fc00::/7
	if len(ip) == net.IPv6len && (ip[0]&0xfe) == 0xfc {
		return true
	}
	return false
}

// FeishuPolicy 返回飞书推送专用策略：只允许官方域名。
func FeishuPolicy() Policy {
	return Policy{
		AllowHosts: []string{"open.feishu.cn", "open.larksuite.com"},
	}
}
