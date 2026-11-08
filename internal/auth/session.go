// Package auth 负责登录态的获取、持久化与校验。
//
// 两种获取方式：
//  1. 浏览器 cookie 导入（主路径，最简单可靠）；
//  2. 微信扫码 OAuth（兜底，cookie 失效时无需手动操作）。
package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Cookie 是持久化的登录态。
//
// 除 sessionid / csrftoken 外的所有 cookie 都保留在 Extra 里：网页端还会带
// university_id、django_language 等辅助 cookie。既然只是只读请求，
// 完整保留比猜哪些有用更稳。
type Cookie struct {
	SessionID string `json:"sessionid"`
	CSRF      string `json:"csrftoken"`
	// Extra 是其余 cookie（键为 cookie 名）。
	Extra map[string]string `json:"extra,omitempty"`
	// Raw 仅用于兼容旧版本只写了 raw 的 cookie.json；新代码不再写入。
	Raw string `json:"raw,omitempty"`
	// UpdatedAt 是登录态获得的时间（导入或扫码成功），后续回写不刷新它。
	UpdatedAt time.Time `json:"updated_at"`
	// RefreshedAt 是最近一次服务端确认会话有效并下发 cookie 的时间。
	//
	// 只要服务端下发任何 cookie 就记一次，而不是只在值变化时记：
	// csrftoken 在同一会话内稳定不变，只看值变化会让这个时间戳永远冻住。
	RefreshedAt time.Time `json:"refreshed_at,omitempty"`
	// UserHint 是登录用户的可读标识，用于在 UI 显示当前登录为谁。
	UserHint string `json:"user_hint,omitempty"`
}

// Valid 报告是否持有最小可用登录态。
func (c Cookie) Valid() bool {
	return strings.TrimSpace(c.SessionID) != "" || strings.TrimSpace(c.Raw) != ""
}

// Apply 把登录态写入请求头。
func (c Cookie) Apply(req *http.Request) {
	if h := c.headerValue(); h != "" {
		req.Header.Set("Cookie", h)
	}
	if c.CSRF != "" {
		req.Header.Set("X-CSRFToken", c.CSRF)
	}
}

// headerValue 拼出 Cookie 请求头的值。
//
// 顺序固定（sessionid → csrftoken → 其余按名排序），并保证同名 cookie
// 只出现一次。这点很重要：单测与抓包对比时，重复的 cookie 会让差异难定位。
func (c Cookie) headerValue() string {
	parts := make([]string, 0, len(c.Extra)+2)
	seen := make(map[string]struct{}, len(c.Extra)+2)
	add := func(k, v string) {
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" || v == "" {
			return
		}
		if _, dup := seen[k]; dup {
			return
		}
		seen[k] = struct{}{}
		parts = append(parts, k+"="+v)
	}

	add("sessionid", c.SessionID)
	add("csrftoken", c.CSRF)
	keys := make([]string, 0, len(c.Extra))
	for k := range c.Extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		add(k, c.Extra[k])
	}
	if len(parts) == 0 {
		return strings.TrimSpace(c.Raw)
	}
	return strings.Join(parts, "; ")
}

// Redacted 返回脱敏描述，用于日志。
func (c Cookie) Redacted() string {
	if c.SessionID == "" {
		return "(无 sessionid)"
	}
	s := c.SessionID
	if len(s) > 8 {
		s = s[:4] + "***" + s[len(s)-2:]
	} else {
		s = "***"
	}
	return "sessionid=" + s
}

// Names 返回持有的全部 cookie 名（不含值），用于排障与 UI 展示。
func (c Cookie) Names() []string {
	names := make([]string, 0, len(c.Extra)+2)
	if c.SessionID != "" {
		names = append(names, "sessionid")
	}
	if c.CSRF != "" {
		names = append(names, "csrftoken")
	}
	for k := range c.Extra {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// mergeCookie 把服务端下发的一个 cookie 合并进当前登录态。
// 返回是否真的发生了变化（只有变化才需要落盘）。
func (c *Cookie) mergeCookie(name, value string) bool {
	if value == "" {
		// 空值不落盘：服务端偶尔用空值表示"清除"，但保留旧值更安全，
		// 而且我们的请求是只读的，多带一个旧 cookie 没有副作用。
		return false
	}
	switch name {
	case "sessionid":
		if c.SessionID == value {
			return false
		}
		c.SessionID = value
		return true
	case "csrftoken":
		if c.CSRF == value {
			return false
		}
		c.CSRF = value
		return true
	}
	if c.Extra == nil {
		c.Extra = make(map[string]string)
	}
	if c.Extra[name] == value {
		return false
	}
	c.Extra[name] = value
	return true
}

// normalize 把旧版本"只存 Raw"的文件迁移成结构化字段。
func (c *Cookie) normalize() {
	raw := strings.TrimSpace(c.Raw)
	if raw == "" {
		return
	}
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch strings.ToLower(k) {
		case "sessionid":
			if c.SessionID == "" {
				c.SessionID = v
			}
		case "csrftoken":
			if c.CSRF == "" {
				c.CSRF = v
			}
		default:
			if c.Extra == nil {
				c.Extra = make(map[string]string)
			}
			if _, exists := c.Extra[k]; !exists {
				c.Extra[k] = v
			}
		}
	}
}

// Store 负责把 Cookie 落盘（0600 权限）。
//
// 并发安全：ykt.Client 会在每个请求的 goroutine 里回调 ObserveCookie，
// 因此所有读写都走互斥锁。
type Store struct {
	mu   sync.RWMutex
	path string
	cur  Cookie

	// 回写节流。ObserveCookie 可能每轮每个响应都被调用，
	// 而磁盘写入是几十毫秒级的同步操作，绝不能每次都写。
	minWriteGap time.Duration
	lastWrite   time.Time
	dirty       bool
}

// DefaultMinWriteGap 是 cookie 回写的最小落盘间隔。
const DefaultMinWriteGap = 30 * time.Second

// NewStore 打开（或初始化）cookie 存储。
func NewStore(path string) (*Store, error) {
	s := &Store{path: path, minWriteGap: DefaultMinWriteGap}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Path 返回存储路径。
func (s *Store) Path() string { return s.path }

// load 从磁盘读取；文件不存在不是错误。
func (s *Store) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取 cookie 失败: %w", err)
	}
	var c Cookie
	if err := json.Unmarshal(b, &c); err != nil {
		return fmt.Errorf("解析 cookie 文件失败（可能已损坏，请重新导入）: %w", err)
	}
	c.normalize()
	c.Extra = cleanExtra(c.Extra)
	s.cur = c
	return nil
}

// Current 返回当前登录态。
func (s *Store) Current() Cookie {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// Valid 报告当前是否持有登录态。
func (s *Store) Valid() bool { return s.Current().Valid() }

// Apply 实现 ykt.Session 接口。
func (s *Store) Apply(req *http.Request) { s.Current().Apply(req) }

// ObserveCookie 实现 ykt.CookieObserver 接口：接收服务端下发的 Set-Cookie。
//
// 雨课堂从不对 sessionid 下发 Set-Cookie，因此这条通路救不了会话过期；
// 但 /v/course_meta/user_info 每次响应都会带 csrftoken。
//
// 两个作用分开看：
//   - 值真的变了 → 记下新值，避免长期使用失效的旧 csrftoken；
//   - 值没变但服务端确实下发了 → 也记一次 RefreshedAt，因为这说明
//     请求真的通过了服务端校验。
func (s *Store) ObserveCookie(name, value string) error {
	name = strings.TrimSpace(name)
	if name == "" || value == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 未登录时不凭空造出登录态：只接受已有登录态之上的增量更新。
	if !s.cur.Valid() {
		return nil
	}

	changed := s.cur.mergeCookie(name, value)
	s.cur.RefreshedAt = time.Now()

	// 值没变就不必写盘（但内存里的时间戳已经更新，面板可见）。
	// 标 dirty 让进程退出时的 Flush 补写，避免每轮抓取都写一次盘。
	if !changed {
		s.dirty = true
		return nil
	}
	return s.persistLocked(true)
}

// Save 持久化新的登录态（原子写入 + 0600）。
//
// UpdatedAt 在这里刷新：它表示"这次登录是什么时候建立的"，
// 后续的 ObserveCookie 回写只更新 RefreshedAt，不会污染这个语义。
func (s *Store) Save(c Cookie) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	c.UpdatedAt = time.Now()
	c.Extra = cleanExtra(c.Extra)

	s.cur = c
	s.dirty = false
	return s.persistLocked(false)
}

// Flush 把内存里尚未落盘的回写写入磁盘。
//
// ObserveCookie 有 30 秒节流，正常情况下最多滞后一次轮询就会落盘；
// 进程退出前主动 Flush 一次，避免刚拿到的 csrftoken 丢掉。
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	return s.persistLocked(false)
}

// persistLocked 把 s.cur 原子写盘。调用方必须持有 s.mu。
//
// throttle 为 true 时，距上次写盘不足 minWriteGap 就只置 dirty 后返回：
// 内存值仍是最新的（请求用的是内存值），磁盘落后一点无妨，
// 下一次 Observe 或 Flush 会补上。
func (s *Store) persistLocked(throttle bool) error {
	if throttle && s.minWriteGap > 0 && time.Since(s.lastWrite) < s.minWriteGap {
		s.dirty = true
		return nil
	}

	b, err := json.MarshalIndent(s.cur, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	// 先写临时文件再 rename，避免中途崩溃留下半个文件。
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("写入 cookie 失败: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("保存 cookie 失败: %w", err)
	}
	s.lastWrite = time.Now()
	s.dirty = false
	return nil
}

// Clear 删除本地登录态。
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cur = Cookie{}
	s.dirty = false
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// cleanExtra 剔除不该出现在 Extra 里的键与空值。
func cleanExtra(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch {
		case k == "", v == "":
			continue
		case strings.EqualFold(k, "sessionid"), strings.EqualFold(k, "csrftoken"):
			// 这两个有专用字段，留在 Extra 里会生成重复 cookie。
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ParseCookieString 解析用户从浏览器复制的 Cookie。
//
// 支持四种粘贴方式：
//   - 完整 Cookie 头："sessionid=xxx; csrftoken=yyy; university_id=zzz"
//   - 只要 sessionid："xxx"
//   - JSON 对象（浏览器插件导出）：{"sessionid":"x","csrftoken":"y"}
//   - DevTools JSON 数组：[{"name":"sessionid","value":"x"}, ...]
func ParseCookieString(raw string) (Cookie, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Cookie{}, fmt.Errorf("cookie 内容为空")
	}

	if c, ok := parseCookieJSON(raw); ok {
		return finalizeCookie(c)
	}

	// 去掉可能包裹的引号
	raw = strings.Trim(raw, `"`+"'")

	c := Cookie{}
	// 逐段解析 k=v
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			// 没有等号：把整串当作 sessionid
			if c.SessionID == "" {
				c.SessionID = part
			}
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch strings.ToLower(k) {
		case "sessionid":
			c.SessionID = v
		case "csrftoken":
			c.CSRF = v
		default:
			if v != "" {
				if c.Extra == nil {
					c.Extra = make(map[string]string)
				}
				c.Extra[k] = v
			}
		}
	}
	if c.SessionID == "" {
		// 最后兜底：整串视为 sessionid（用户只复制了值）
		if !strings.Contains(raw, "=") {
			c.SessionID = raw
		}
	}
	return finalizeCookie(c)
}

// parseCookieJSON 解析两种常见的浏览器导出 JSON 形式。
//
// ok=false 表示"不是 JSON 形式"或结构不认识，交给普通 Cookie 头解析，
// 避免误伤。
func parseCookieJSON(raw string) (Cookie, bool) {
	head := raw[0]
	if head != '{' && head != '[' {
		return Cookie{}, false
	}

	c := Cookie{}
	if head == '{' {
		var kv map[string]string
		if err := json.Unmarshal([]byte(raw), &kv); err != nil {
			return Cookie{}, false
		}
		for k, v := range kv {
			_ = c.mergeCookie(strings.TrimSpace(k), strings.TrimSpace(v))
		}
		return c, true
	}

	var arr []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal([]byte(raw), &arr); err != nil || len(arr) == 0 {
		return Cookie{}, false
	}
	for _, item := range arr {
		_ = c.mergeCookie(strings.TrimSpace(item.Name), strings.TrimSpace(item.Value))
	}
	return c, true
}

// finalizeCookie 校验最小可用性并清理 Extra。
func finalizeCookie(c Cookie) (Cookie, error) {
	c.Extra = cleanExtra(c.Extra)
	if strings.TrimSpace(c.SessionID) == "" {
		return Cookie{}, fmt.Errorf("未找到 sessionid，请确认复制的是完整 Cookie（应包含 sessionid=...）")
	}
	return c, nil
}
