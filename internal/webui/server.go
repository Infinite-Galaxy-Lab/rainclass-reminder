package webui

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/infinite-galaxy/rainclass-reminder/internal/auth"
	"github.com/infinite-galaxy/rainclass-reminder/internal/config"
	"github.com/infinite-galaxy/rainclass-reminder/internal/notify"
	"github.com/infinite-galaxy/rainclass-reminder/internal/qrutil"
	"github.com/infinite-galaxy/rainclass-reminder/internal/store"
	"github.com/infinite-galaxy/rainclass-reminder/internal/ykt"
)

//go:embed assets/index.html assets/app.js assets/style.css
var assets embed.FS

// Server 是本地 Web 面板。
//
// 安全设计：
//   - 默认只监听 127.0.0.1，不对外暴露；
//   - 若配置了 Listen 为非回环地址，必须同时配置 Token，否则拒绝启动；
//   - 所有接口都是 GET/JSON，唯一"写"操作是扫码登录与刷新，
//     不提供任何修改雨课堂数据的入口。
type Server struct {
	cfg     config.Config
	client  *ykt.Client
	auth    *auth.Store
	repo    store.Repository
	notify  *notify.Registry
	log     *slog.Logger
	version string

	mu      sync.Mutex
	qr      *qrSession
	refresh chan struct{}
}

// qrSession 是一次进行中的扫码登录。
type qrSession struct {
	PNG       []byte
	UUID      string
	State     string
	ExpiresAt time.Time
	Status    string
	Done      bool
	Err       string
}

// Options 是面板构造参数。
type Options struct {
	Config config.Config
	Client *ykt.Client
	Auth   *auth.Store
	Repo   store.Repository
	Notify *notify.Registry
	Logger *slog.Logger
	// Version 是程序版本，由 cli 注入（webui 不能 import cli，会成环）。
	Version string
}

// New 构造面板服务。
func New(opts Options) (*Server, error) {
	listen := opts.Config.WebUI.Listen
	if listen == "" {
		listen = "127.0.0.1:8787"
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return nil, fmt.Errorf("web_ui.listen 非法（应为 host:port）: %w", err)
	}
	// 非回环地址必须带 token，否则等于把面板暴露到局域网。
	if !isLoopback(host) && strings.TrimSpace(opts.Config.WebUI.Token) == "" {
		return nil, fmt.Errorf("web_ui.listen 绑定到非本机地址 %q 时必须配置 web_ui.token", host)
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Server{
		cfg:     opts.Config,
		client:  opts.Client,
		auth:    opts.Auth,
		repo:    opts.Repo,
		notify:  opts.Notify,
		log:     opts.Logger,
		version: opts.Version,
		refresh: make(chan struct{}, 1),
	}, nil
}

// Handler 返回 HTTP 路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/status", s.guard(s.handleStatus))
	mux.HandleFunc("/api/tasks", s.guard(s.handleTasks))
	mux.HandleFunc("/api/history", s.guard(s.handleHistory))
	mux.HandleFunc("/api/qr/start", s.guard(s.handleQRStart))
	mux.HandleFunc("/api/qr/poll", s.guard(s.handleQRPoll))
	mux.HandleFunc("/api/refresh", s.guard(s.handleRefresh))
	mux.HandleFunc("/assets/", s.handleAsset)
	return mux
}

// guard 做访问控制：非回环来源必须提供正确 token。
func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			http.Error(w, "未授权：请通过 ?token=... 访问", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// authorized 判断请求是否允许。
func (s *Server) authorized(r *http.Request) bool {
	token := strings.TrimSpace(s.cfg.WebUI.Token)
	if token == "" {
		return true // 仅回环监听，无需 token
	}
	got := r.URL.Query().Get("token")
	if got == "" {
		got = r.Header.Get("X-Panel-Token")
	}
	// 常量时间比较，避免通过响应时间差逐字符猜测 token。
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

// isLoopback 判断 host 是否为回环。
func isLoopback(host string) bool {
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := assets.ReadFile("assets/index.html")
	if err != nil {
		http.Error(w, "页面加载失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// 面板是本地工具页，禁止被 iframe 嵌入。
	w.Header().Set("X-Frame-Options", "DENY")
	_, _ = w.Write(b)
}

func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	// 只允许访问 assets 目录下的已知文件，杜绝路径穿越。
	switch name {
	case "assets/app.js":
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case "assets/style.css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	default:
		http.NotFound(w, r)
		return
	}
	b, err := assets.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write(b)
}

// statusResponse 是 /api/status 的响应。
type statusResponse struct {
	Version       string    `json:"version"`
	LoggedIn      bool      `json:"logged_in"`
	User          string    `json:"user"`
	CookieUpdated time.Time `json:"cookie_updated"`
	// CookieRefreshedAt 是最近一次收到服务端 Set-Cookie 的时间。
	//
	// 面板用它回答"这份登录态还活着吗"：sessionid 本身不会被续期，
	// 但 csrftoken 会被周期性下发，这个时间能证明请求确实通过了服务端校验。
	CookieRefreshedAt time.Time `json:"cookie_refreshed_at"`
	// CookieCount 是持有的 cookie 个数。少于 2 说明只导入了 sessionid，
	// 缺少 csrftoken 等辅助字段，部分接口可能降级。
	CookieCount     int       `json:"cookie_count"`
	Channels        []string  `json:"channels"`
	DataSourceOK    bool      `json:"datasource_ok"`
	DataSourceErr   string    `json:"datasource_err,omitempty"`
	TaskCount       int       `json:"task_count"`
	LastFetch       time.Time `json:"last_fetch"`
	Now             time.Time `json:"now"`
	IntervalMinutes int       `json:"interval_minutes"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	c := s.auth.Current()
	resp := statusResponse{
		Version:           s.version,
		LoggedIn:          c.Valid(),
		User:              c.UserHint,
		CookieUpdated:     c.UpdatedAt,
		CookieRefreshedAt: c.RefreshedAt,
		CookieCount:       len(c.Names()),
		Channels:          s.channelNames(),
		Now:               time.Now(),
		IntervalMinutes:   int(s.cfg.Poll.Interval.Minutes()),
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	// 在线校验登录态（失败不致命，只是标记）。
	if c.Valid() {
		if info, err := s.client.BasicInfo(ctx); err == nil {
			name := info.Nickname.String()
			if name == "" {
				name = info.Name.String()
			}
			resp.User = name
			resp.DataSourceOK = true
		} else {
			resp.DataSourceErr = err.Error()
		}
	}

	snap, err := s.repo.LoadTasks(ctx)
	if err == nil {
		resp.TaskCount = len(snap.Tasks)
		resp.LastFetch = snap.FetchedAt
	}
	writeJSON(w, resp)
}

// channelNames 返回已启用的推送通道名。
func (s *Server) channelNames() []string {
	if s.notify == nil {
		return []string{}
	}
	names := s.notify.Names()
	if names == nil {
		return []string{}
	}
	return names
}

// taskItem 是面板展示用的任务项。
type taskItem struct {
	ID               string `json:"id"`
	Type             string `json:"type"`
	TypeLabel        string `json:"type_label"`
	Title            string `json:"title"`
	Course           string `json:"course"`
	Deadline         string `json:"deadline"`
	DeadlineISO      string `json:"deadline_iso"`
	Remaining        string `json:"remaining"`
	RemainingSeconds int64  `json:"remaining_seconds"`
	Status           string `json:"status"`
	URL              string `json:"url"`
	Expired          bool   `json:"expired"`
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	snap, err := s.repo.LoadTasks(ctx)
	if err != nil {
		http.Error(w, "读取任务失败", http.StatusInternalServerError)
		return
	}

	now := time.Now()
	items := make([]taskItem, 0, len(snap.Tasks))
	for _, t := range snap.Tasks {
		it := taskItem{
			ID:        t.ID,
			Type:      string(t.Type),
			TypeLabel: t.Type.Label(),
			Title:     t.Title,
			Course:    t.Course,
			Status:    string(t.Status),
			URL:       t.URL,
			Remaining: t.Remaining(now),
			Expired:   t.IsExpired(now),
		}
		if t.HasDeadline() {
			it.Deadline = t.Deadline.Format("01-02 15:04")
			it.DeadlineISO = t.Deadline.Format(time.RFC3339)
			it.RemainingSeconds = int64(t.TimeLeft(now).Seconds())
		}
		items = append(items, it)
	}

	writeJSON(w, map[string]any{
		"tasks":      items,
		"fetched_at": snap.FetchedAt,
		"now":        now,
	})
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	recs, err := s.repo.RecentPushes(ctx, 50)
	if err != nil {
		http.Error(w, "读取历史失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"records": recs})
}

// handleQRStart 启动一次扫码登录并返回二维码 PNG（base64 data URL）。
func (s *Server) handleQRStart(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.qr != nil && !s.qr.Done && time.Now().Before(s.qr.ExpiresAt) {
		// 复用未过期的二维码
		pngB64 := encodePNGBase64(s.qr.PNG)
		s.mu.Unlock()
		writeJSON(w, map[string]any{"png": pngB64, "expires_at": s.qr.ExpiresAt})
		return
	}
	s.mu.Unlock()

	qr, err := auth.NewQRLogin(s.cfg.Ykt.BaseURL, s.client.HTTPClient())
	if err != nil {
		http.Error(w, "初始化扫码失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	code, err := qr.Start(r.Context())
	if err != nil {
		http.Error(w, "申请二维码失败: "+err.Error(), http.StatusBadGateway)
		return
	}

	// 微信接口返回 JPEG，浏览器 <img> 虽可直接显示，
	// 但统一转 PNG 可避免前端对格式的隐式依赖。
	qrPNG, err := qrutil.ToPNG(code.PNG)
	if err != nil {
		http.Error(w, "二维码转码失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	s.qr = &qrSession{
		PNG:       qrPNG,
		UUID:      code.UUID,
		State:     code.State,
		ExpiresAt: code.ExpiresAt,
		Status:    "等待扫码",
	}
	s.mu.Unlock()

	// 后台轮询，把结果写入 session。
	go s.pollQR(qr, code)

	writeJSON(w, map[string]any{
		"png":        encodePNGBase64(qrPNG),
		"expires_at": code.ExpiresAt,
	})
}

// pollQR 在后台等待扫码并保存登录态。
func (s *Server) pollQR(qr *auth.QRLogin, code auth.QRCode) {
	ctx, cancel := context.WithDeadline(context.Background(), code.ExpiresAt)
	defer cancel()

	res, err := qr.Poll(ctx, code, 2*time.Second, func(status string) {
		s.mu.Lock()
		if s.qr != nil {
			s.qr.Status = status
		}
		s.mu.Unlock()
	})

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.qr == nil {
		return
	}
	s.qr.Done = true
	if err != nil {
		s.qr.Err = err.Error()
		s.qr.Status = "登录失败"
		return
	}
	if err := s.auth.Save(res.Cookie); err != nil {
		s.qr.Err = "保存登录态失败: " + err.Error()
		return
	}
	s.qr.Status = "登录成功"
	s.log.Info("扫码登录成功")
}

// handleQRPoll 返回扫码进度。
func (s *Server) handleQRPoll(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.qr == nil {
		writeJSON(w, map[string]any{"active": false})
		return
	}
	writeJSON(w, map[string]any{
		"active":     !s.qr.Done,
		"status":     s.qr.Status,
		"error":      s.qr.Err,
		"done":       s.qr.Done,
		"expires_at": s.qr.ExpiresAt,
	})
}

// handleRefresh 触发一次立即刷新（由主循环消费）。
//
// 响应里带 requested_at：前端据此判断"抓取是否已完成"，
// 比固定等 2.5 秒可靠得多（一轮抓取要几十秒）。
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	queued := false
	select {
	case s.refresh <- struct{}{}:
	default:
		queued = true
	}
	writeJSON(w, map[string]any{
		"ok":           true,
		"queued":       queued,
		"requested_at": now,
		"message":      "已请求刷新",
	})
}

// RefreshC 返回刷新信号通道。
func (s *Server) RefreshC() <-chan struct{} { return s.refresh }

// ListenAndServe 启动 HTTP 服务。
func (s *Server) ListenAndServe(ctx context.Context) error {
	addr := s.cfg.WebUI.Listen
	if addr == "" {
		addr = "127.0.0.1:8787"
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		s.log.Info("本地面板已启动", "url", "http://"+addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

// writeJSON 输出 JSON。
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// encodePNGBase64 把 PNG 转成 data URL，供前端 <img> 直接显示。
func encodePNGBase64(png []byte) string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
}
