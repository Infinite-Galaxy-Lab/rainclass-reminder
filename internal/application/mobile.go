package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/auth"
	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/domain"
	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/notify"
	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/scheduler"
	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/store"
	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/ykt"
)

const BaseURL = "https://changjiang.yuketang.cn"

type Sink interface {
	Notify(title, body string) error
}

type Mobile struct {
	mu        sync.Mutex
	cancelMu  sync.Mutex
	cancel    context.CancelFunc
	repo      *store.SQLite
	session   *session
	client    *ykt.Client
	sink      Sink
	authState string
	closed    bool
	options   ykt.Options
	location  *time.Location
	clock     func() time.Time
}

type TaskView struct {
	ID         string   `json:"id"`
	Type       string   `json:"type"`
	Label      string   `json:"label"`
	Title      string   `json:"title"`
	Course     string   `json:"course"`
	Deadline   int64    `json:"deadline"`
	StartAt    int64    `json:"start_at"`
	Status     string   `json:"status"`
	Score      *float64 `json:"score"`
	TotalScore *float64 `json:"total_score"`
	URL        string   `json:"url"`
}

type Snapshot struct {
	SchemaVersion int        `json:"schema_version"`
	AuthState     string     `json:"auth_state"`
	FetchedAt     int64      `json:"fetched_at"`
	Tasks         []TaskView `json:"tasks"`
}

func OpenMobile(dir string, vault Vault, sink Sink, zone string) (*Mobile, error) {
	location, err := time.LoadLocation(zone)
	if err != nil || zone == "" || zone == "Local" {
		return nil, errors.New("timezone")
	}
	m, err := openMobile(dir, vault, sink, ykt.Options{BaseURL: BaseURL})
	if err == nil {
		m.location = location
	}
	return m, err
}

func openMobile(dir string, vault Vault, sink Sink, options ykt.Options) (*Mobile, error) {
	if vault == nil || sink == nil {
		return nil, errors.New("platform_required")
	}
	value, err := vault.Load()
	if err != nil {
		return nil, errors.New("vault")
	}
	sess := &session{vault: vault}
	if value != "" {
		var saved credentials
		if err := json.Unmarshal([]byte(value), &saved); err != nil || saved.Account == "" || !saved.Cookie.Valid() {
			return nil, errors.New("vault")
		}
		sess.cookie, sess.account = saved.Cookie, saved.Account
	}
	repo, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		return nil, errors.New("storage")
	}
	// 两个存储不能原子提交；重开时对齐账号，防止中断切换后暴露上一账号快照。
	if sess.Valid() {
		previous, err := repo.GetKV(context.Background(), "mobile_account")
		if err == nil && previous != sess.account {
			err = repo.Reset(context.Background())
			if err == nil {
				err = repo.SetKV(context.Background(), "mobile_account", sess.account)
			}
		}
		if err != nil {
			repo.Close()
			return nil, errors.New("storage")
		}
	}
	options.Session = sess
	options.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	client, err := ykt.New(options)
	if err != nil {
		repo.Close()
		return nil, errors.New("network")
	}
	state := "signed_out"
	if sess.Valid() {
		state = "ready"
	}
	return &Mobile{repo: repo, session: sess, client: client, sink: sink, authState: state, options: options,
		location: time.Local, clock: time.Now}, nil
}

func (m *Mobile) SetTimeZone(zone string) error {
	location, err := time.LoadLocation(zone)
	if err != nil || zone == "" || zone == "Local" {
		return errors.New("timezone")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("closed")
	}
	m.location = location
	return nil
}

func (m *Mobile) Authenticate(raw string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("closed")
	}
	cookie, err := auth.ParseCookieString(raw)
	if err != nil {
		return errors.New("invalid_session")
	}
	ctx, done := m.operation()
	defer done()
	candidate := &session{cookie: cookie}
	opts := m.options
	opts.Session = candidate
	client, err := ykt.New(opts)
	if err != nil {
		return errors.New("network")
	}
	info, err := client.BasicInfo(ctx)
	if err != nil {
		return classify(err)
	}
	account := info.UserID.String()
	if account == "" || account == "0" {
		return errors.New("invalid_session")
	}
	previous, err := m.repo.GetKV(ctx, "mobile_account")
	if err != nil {
		return errors.New("storage")
	}
	candidate.cookie.UpdatedAt = time.Now()
	candidate.vault, candidate.account = m.session.vault, account
	if err := candidate.persist(); err != nil {
		return errors.New("vault")
	}
	// 先持久化已验证的会话；重开时会修复跨存储中断，保存失败不会清空旧数据。
	if previous != account {
		if err := m.repo.Reset(ctx); err != nil {
			return errors.New("storage")
		}
	}
	if err := m.repo.SetKV(ctx, "mobile_account", account); err != nil {
		return errors.New("storage")
	}
	m.session, m.client, m.authState = candidate, client, "ready"
	return nil
}

func (m *Mobile) Refresh() (string, error) {
	return m.RefreshContext(context.Background())
}

func (m *Mobile) RefreshContext(parent context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", errors.New("closed")
	}
	if err := parent.Err(); err != nil {
		return "", classify(err)
	}
	if !m.session.Valid() {
		return "", errors.New("session_required")
	}
	ctx, done := m.operationContext(parent)
	defer done()
	registry, _, _ := notify.NewRegistry([]string{"android"}, map[string]func() (notify.Notifier, error){
		"android": func() (notify.Notifier, error) { return mobileNotifier{m.sink}, nil },
	})
	sch, err := scheduler.New(scheduler.Options{
		Store: m.repo, Source: &scheduler.YktSource{Client: m.client, DetailDays: 7},
		Notify: registry, Policy: domain.DefaultPolicyConfig(), Logger: m.options.Logger,
		Clock: func() time.Time { return m.clock().In(m.location) },
	})
	if err != nil {
		return "", errors.New("storage")
	}
	if err := m.client.CheckAuth(ctx); err != nil {
		if ctx.Err() != nil {
			return "", classify(ctx.Err())
		}
		if ykt.IsAuthError(err) {
			m.authState = "expired"
			_ = sch.AlertAuthExpired(ctx)
		}
		return "", classify(err)
	}
	m.authState = "ready"
	result := sch.RunCycle(ctx)
	if result.Err != nil {
		if ctx.Err() != nil {
			return "", classify(ctx.Err())
		}
		if ykt.IsAuthError(result.Err) {
			m.authState = "expired"
		}
		return "", classify(result.Err)
	}
	return m.snapshot(ctx)
}

func (m *Mobile) Snapshot() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", errors.New("closed")
	}
	return m.snapshot(context.Background())
}

func (m *Mobile) snapshot(ctx context.Context) (string, error) {
	view := Snapshot{SchemaVersion: 1, AuthState: m.authState, Tasks: []TaskView{}}
	if m.session.Valid() {
		snapshot, err := m.repo.LoadTasks(ctx)
		if err != nil {
			return "", errors.New("storage")
		}
		view.FetchedAt = milliseconds(snapshot.FetchedAt)
		for _, task := range snapshot.Tasks {
			view.Tasks = append(view.Tasks, TaskView{
				ID: task.ID, Type: string(task.Type), Label: task.Type.Label(), Title: task.Title, Course: task.Course,
				Deadline: milliseconds(task.Deadline), StartAt: milliseconds(task.StartAt), Status: string(task.Status),
				Score: task.Score, TotalScore: task.TotalScore, URL: task.URL,
			})
		}
	}
	value, err := json.Marshal(view)
	return string(value), err
}

func (m *Mobile) SignOut() error {
	m.Cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("closed")
	}
	if err := m.session.vault.Save(""); err != nil {
		return errors.New("vault")
	}
	m.session = &session{vault: m.session.vault}
	m.client.SetSession(m.session)
	m.authState = "signed_out"
	if err := m.repo.Reset(context.Background()); err != nil {
		return errors.New("storage")
	}
	return nil
}

func (m *Mobile) Cancel() {
	m.cancelMu.Lock()
	defer m.cancelMu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
}

func (m *Mobile) Close() error {
	m.Cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	return m.repo.Close()
}

func (m *Mobile) operation() (context.Context, func()) {
	return m.operationContext(context.Background())
}

func (m *Mobile) operationContext(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	m.cancelMu.Lock()
	m.cancel = cancel
	m.cancelMu.Unlock()
	return ctx, func() {
		cancel()
		m.cancelMu.Lock()
		m.cancel = nil
		m.cancelMu.Unlock()
	}
}

type mobileNotifier struct{ sink Sink }

func (mobileNotifier) Name() string { return "android" }

func (n mobileNotifier) Send(ctx context.Context, message notify.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	body := message.Markdown
	if message.Title == "🔑 需要重新登录雨课堂" {
		body = "登录已失效，请打开应用重新登录。截止提醒暂时暂停。"
	}
	if err := n.sink.Notify(message.Title, body); err != nil {
		return errors.New("notification_unavailable")
	}
	return nil
}

func classify(err error) error {
	switch {
	case ykt.IsAuthError(err):
		return errors.New("auth_expired")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return errors.New("canceled")
	case errors.Is(err, ykt.ErrRateLimited):
		return errors.New("rate_limited")
	default:
		return errors.New("network")
	}
}

func milliseconds(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return at.UnixMilli()
}
