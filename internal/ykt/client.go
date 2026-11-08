package ykt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Session 是登录态的最小抽象：只需要"能把 cookie 塞进请求"。
type Session interface {
	// Apply 把当前登录态写入请求头。
	Apply(req *http.Request)
	// Valid 报告是否持有可用登录态（不做网络校验）。
	Valid() bool
}

// CookieObserver 接收服务端下发的 Set-Cookie。
//
// 回调只收 name/value，不把 Domain/Path 语义带进存储层。
type CookieObserver interface {
	ObserveCookie(name, value string) error
}

// observe 把 Set-Cookie 交给观察者；Session 没实现该接口时静默跳过。
func (c *Client) observe(resp *http.Response) {
	obs, ok := c.CurrentSession().(CookieObserver)
	if !ok || obs == nil {
		return
	}
	for _, sc := range resp.Cookies() {
		if strings.TrimSpace(sc.Name) == "" {
			continue
		}
		if err := obs.ObserveCookie(sc.Name, sc.Value); err != nil {
			// 回写失败不影响抓取：内存里的登录态依然可用。
			c.log.Warn("登录态回写失败", "cookie", sc.Name, "err", err)
		}
	}
}

// sessionBox 让 atomic.Pointer 能持有接口值（它的元素必须是指针类型）。
type sessionBox struct{ s Session }

// Client 是雨课堂只读 API 客户端，可被多 goroutine 共享。
type Client struct {
	baseURL   string
	identity  int
	userAgent string
	// http 用于业务请求：不带 cookie jar，Cookie 头完全由 Session 决定。
	http *http.Client
	// authHTTP 用于扫码登录：带 cookie jar，跨步骤自动携带 csrftoken。
	authHTTP *http.Client
	sem      chan struct{}
	log      *slog.Logger
	// session 用原子指针持有：applyHeaders 并发读，SetSession 可能从
	// 另一个 goroutine 写。
	session atomic.Pointer[sessionBox]

	// 章节树缓存（按 classroom_id）。
	chapterMu    sync.Mutex
	chapterCache map[string]chapterCacheEntry
	chapterTTL   time.Duration

	// 详情缓存（按 classroom_id + leafID）。详情是一轮里请求量最大的一环，
	// 而截止时间在短时间内变化极小，缓存能降八成以上请求。
	detailMu       sync.Mutex
	detailCache    map[string]detailCacheEntry
	detailTTL      time.Duration
	detailTTLTiers []CacheTier

	// 学习日志缓存（按 classroom_id）。每门课一次请求、最多翻两页，
	// 比逐叶子拉详情便宜得多，ID 一旦生成也基本不变，因此 TTL 给得长。
	logMu    sync.Mutex
	logCache map[string]logCacheEntry
	logTTL   time.Duration

	// 全局限速：并发信号量只限制"同时几个在飞"，挡不住 4 个请求一起打到
	// 对端。这里再加一道节流把流量摊平，降低被风控的概率。
	paceMu   sync.Mutex
	lastSend time.Time
	gap      time.Duration
	observer Observer
}

// CacheTier 是"距截止 ≤ Within 时，缓存 TTL 用 TTL"的一档。
// Within=0 表示兜底档。
type CacheTier struct {
	Within time.Duration
	TTL    time.Duration
}

// Options 是构造客户端所需参数。
type Options struct {
	BaseURL     string
	Identity    int
	UserAgent   string
	Timeout     time.Duration
	Concurrency int
	ChapterTTL  time.Duration
	// DetailTTL 是叶子详情缓存时长；<=0 时用 60m。
	DetailTTL time.Duration
	// LearnLogTTL 是学习日志缓存时长；<=0 时用 6h。
	LearnLogTTL time.Duration
	// DetailTTLTiers 按距截止远近分档缓存详情，越近刷得越勤。
	// 为空时全部使用 DetailTTL。
	DetailTTLTiers []CacheTier
	// MinRequestGap 是全局请求最小间隔；<=0 时用 300ms。
	MinRequestGap time.Duration
	Session       Session
	Logger        *slog.Logger
	Transport     http.RoundTripper
	Observer      Observer
}

// New 构造客户端。
//
// 两个 http.Client 刻意分开：api 不带 cookie jar，登录态完全由
// Session.Apply 写入；authFlow 带 jar，因为扫码登录需要跨步骤自动携带
// csrftoken。若给 api 也挂 jar，jar 里吸收的 Set-Cookie 会被 net/http
// 追加到同一个 Cookie 头，产生重复 cookie。
func New(opts Options) (*Client, error) {
	if opts.BaseURL == "" {
		return nil, fmt.Errorf("baseURL 不能为空")
	}
	if _, err := url.Parse(opts.BaseURL); err != nil {
		return nil, fmt.Errorf("baseURL 非法: %w", err)
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 20 * time.Second
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.ChapterTTL <= 0 {
		opts.ChapterTTL = 30 * time.Minute
	}
	if opts.Identity == 0 {
		opts.Identity = 2
	}
	if opts.DetailTTL <= 0 {
		opts.DetailTTL = 60 * time.Minute
	}
	if opts.LearnLogTTL <= 0 {
		opts.LearnLogTTL = 6 * time.Hour
	}
	if opts.MinRequestGap <= 0 {
		opts.MinRequestGap = 300 * time.Millisecond
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}

	transport := opts.Transport
	if transport == nil {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	}
	// 不自动跟随重定向：雨课堂用 302 表示登录失败/跳转，需要显式判断。
	noRedirect := func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	hc := &http.Client{
		Timeout:       opts.Timeout,
		Transport:     transport,
		CheckRedirect: noRedirect,
	}
	// 登录流程独立 client，共享同一个 Transport（连接池复用）。
	authClient := &http.Client{
		Timeout:       opts.Timeout,
		Jar:           jar,
		Transport:     transport,
		CheckRedirect: noRedirect,
	}

	c := &Client{
		baseURL:        strings.TrimRight(opts.BaseURL, "/"),
		identity:       opts.Identity,
		userAgent:      opts.UserAgent,
		http:           hc,
		authHTTP:       authClient,
		sem:            make(chan struct{}, opts.Concurrency),
		log:            opts.Logger,
		chapterCache:   make(map[string]chapterCacheEntry),
		chapterTTL:     opts.ChapterTTL,
		detailCache:    make(map[string]detailCacheEntry),
		detailTTL:      opts.DetailTTL,
		detailTTLTiers: opts.DetailTTLTiers,
		logCache:       make(map[string]logCacheEntry),
		logTTL:         opts.LearnLogTTL,
		gap:            opts.MinRequestGap,
		observer:       opts.Observer,
	}
	if opts.Session != nil {
		c.session.Store(&sessionBox{s: opts.Session})
	}
	return c, nil
}

// HTTPClient 返回供扫码登录复用的 client（带 cookie jar）。
//
// 注意：这个 client 只用于登录流程，不要拿它发业务请求——
// 它带 jar，会和 Session 手工设置的 Cookie 头叠加。
func (c *Client) HTTPClient() *http.Client { return c.authHTTP }

// SetSession 在扫码登录完成后替换登录态。
//
// 并发安全：session 用 atomic.Pointer 持有，因为 applyHeaders 会在
// 每个请求的 goroutine 里读它，而 SetSession 可能被面板的扫码流程
// 从另一个 goroutine 调用。直接赋值会有数据竞争。
func (c *Client) SetSession(s Session) {
	if s == nil {
		c.session.Store(nil)
		return
	}
	c.session.Store(&sessionBox{s: s})
	// 换账号/重新登录后，旧缓存可能属于另一个用户，必须清掉。
	c.invalidateChapterCache()
	c.invalidateDetailCache()
}

// CurrentSession 返回当前登录态（可能为 nil）。
func (c *Client) CurrentSession() Session {
	if b := c.session.Load(); b != nil {
		return b.s
	}
	return nil
}

// BaseURL 返回站点根地址（用于拼接直达链接）。
func (c *Client) BaseURL() string { return c.baseURL }

// getJSON 发起只读 GET 并解析 JSON。
//
// 这里承载了所有与雨课堂通信的细节：并发限流、请求头、错误分类。
// 业务方法只需关心"路径参数 + 目标结构体"。
func (c *Client) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	return c.getJSONScoped(ctx, path, query, "", out)
}

// getJSONScoped 是 getJSON 的带作用域版本，classroomID 非空时附加
// classroom-id 请求头。
//
// /leaf_info 缺这个头时返回 HTTP 200 但 error_code=40000，属于
// "HTTP 成功但业务失败"，只看状态码会误判为正常。
func (c *Client) getJSONScoped(ctx context.Context, path string, query url.Values, classroomID string, out any) (resultErr error) {
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return ctx.Err()
	}

	// 全局限速：无论并发多少，实际发请求都被摊平成 ≤ 1/gap 的速率。
	// 注意这里在持有信号量之后等待，等价于"排队式"节流。
	if err := c.pace(ctx); err != nil {
		return err
	}

	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("%w: 构造请求失败: %v", ErrUpstream, err)
	}
	c.applyHeaders(req)
	if classroomID != "" {
		req.Header.Set("classroom-id", classroomID)
	}

	event := RequestObservation{At: time.Now(), Endpoint: observationEndpoint(path)}
	if c.observer != nil {
		defer func() {
			event.DurationMS = time.Since(event.At).Milliseconds()
			event.Outcome = observationOutcome(resultErr)
			c.observer.Request(event)
		}()
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: 请求 %s 失败: %v", ErrUpstream, path, err)
	}
	event.Status = resp.StatusCode
	if c.observer != nil {
		recordResponseCookies(&event, resp)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
	}()

	// 先吸收 Set-Cookie 再看状态码：即使是 4xx/5xx，服务端也可能顺手
	// 刷新 csrftoken，丢掉就白丢了一次续期机会。
	c.observe(resp)

	switch code := resp.StatusCode; {
	case code == http.StatusUnauthorized:
		return ErrAuthExpired
	case code == http.StatusForbidden:
		return ErrAuthExpired
	case code == http.StatusTooManyRequests:
		return ErrRateLimited
	case code == http.StatusNotFound:
		return ErrNotFound
	case code >= 300 && code < 400:
		// 雨课堂用 302 跳登录页表示 cookie 失效。
		return ErrAuthExpired
	case code >= 500:
		return fmt.Errorf("%w: 服务端 %d", ErrUpstream, code)
	case code != http.StatusOK:
		return fmt.Errorf("%w: 未预期状态码 %d", ErrUpstream, code)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("%w: 读取响应失败: %v", ErrUpstream, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		// 返回 HTML 通常意味着被重定向到登录页但没给 302。
		if looksLikeHTML(body) {
			return ErrAuthExpired
		}
		return fmt.Errorf("%w: 解析 JSON 失败: %v", ErrUpstream, err)
	}
	return nil
}

// applyHeaders 统一设置雨课堂 Web 端所需的请求头。
func (c *Client) applyHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("xtbz", "ykt")
	req.Header.Set("Referer", c.baseURL+webHomeURL)
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	if s := c.CurrentSession(); s != nil && s.Valid() {
		s.Apply(req)
	}
}

// pace 实现全局最小请求间隔。
//
// 设计：持有 paceMu 算出"该睡多久"，睡完立刻更新 lastSend 并释放锁，
// 这样多个并发请求会串成一条均匀的时间线，而不是一起醒来再一起发。
func (c *Client) pace(ctx context.Context) error {
	if c.gap <= 0 {
		return nil
	}
	c.paceMu.Lock()
	wait := time.Duration(0)
	if !c.lastSend.IsZero() {
		if elapsed := time.Since(c.lastSend); elapsed < c.gap {
			wait = c.gap - elapsed
		}
	}
	// 先占位：把本次请求的发送时刻预定在 wait 之后，
	// 让后续请求基于这个预定位来计算，避免全部同时通过。
	c.lastSend = time.Now().Add(wait)
	c.paceMu.Unlock()

	if wait <= 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// looksLikeHTML 粗判响应体是否为 HTML（用于识别被重定向到登录页）。
//
// 只认 doctype 与 <html 两种前缀：放宽到"任意以 < 开头"会把正常的
// XML 或非 JSON 片段误判成登录页，导致误报登录失效。
func looksLikeHTML(b []byte) bool {
	head := strings.ToLower(strings.TrimSpace(string(b[:min(len(b), 512)])))
	return strings.HasPrefix(head, "<!doctype") || strings.HasPrefix(head, "<html")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// chapterCacheEntry 是章节树的缓存项。
type chapterCacheEntry struct {
	at   time.Time
	data *ChapterData
}

// detailCacheEntry 是叶子详情的缓存项。
type detailCacheEntry struct {
	at   time.Time
	data LeafInfoData
}

// logCacheEntry 是学习日志的缓存项。
type logCacheEntry struct {
	at   time.Time
	data []LearnLogActivity
}

func (c *Client) invalidateChapterCache() {
	c.chapterMu.Lock()
	defer c.chapterMu.Unlock()
	c.chapterCache = make(map[string]chapterCacheEntry)
}

func (c *Client) invalidateDetailCache() {
	c.detailMu.Lock()
	defer c.detailMu.Unlock()
	c.detailCache = make(map[string]detailCacheEntry)
}

// ttlFor 按"距截止还剩多久"选择详情缓存时长。
//
// deadline 为零值表示"还不知道截止时间"（章节树没给），此时用最短档，
// 因为它最需要尽快通过详情补全。
func (c *Client) ttlFor(deadline time.Time) time.Duration {
	if len(c.detailTTLTiers) == 0 {
		return c.detailTTL
	}
	// 未知截止：用最细的档，尽快确认真实截止时间。
	if deadline.IsZero() {
		return c.detailTTLTiers[0].TTL
	}
	left := time.Until(deadline)
	if left < 0 {
		// 已过期：保持较短 TTL，以便及时看到"完成了没"。
		return c.detailTTLTiers[0].TTL
	}
	for _, t := range c.detailTTLTiers {
		// Within=0 是兜底档，放最后匹配。
		if t.Within > 0 && left <= t.Within {
			return t.TTL
		}
	}
	for _, t := range c.detailTTLTiers {
		if t.Within == 0 {
			return t.TTL
		}
	}
	return c.detailTTL
}

// taskURL 按任务类型生成网页端详情页直达链接。
//
// 路由与兜底规则统一在 webTaskURL（mapper.go）；客户端方法只提供 baseURL。
// 注：链接生成统一在 mapper.go 的 leafURL / taskURLByKind。
// 客户端不再保留"按业务类型拼 URL"的方法——业务类型与路由是两件事。
