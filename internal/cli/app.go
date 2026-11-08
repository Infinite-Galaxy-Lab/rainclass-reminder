// Package cli 实现命令行子命令分发。
//
// 只依赖标准库 flag，避免引入 cobra 等重量级依赖。
// 每个子命令一个文件，共享 app.go 中的依赖装配。
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/2963609262/ykt-reminder/internal/auth"
	"github.com/2963609262/ykt-reminder/internal/config"
	"github.com/2963609262/ykt-reminder/internal/domain"
	"github.com/2963609262/ykt-reminder/internal/notify"
	"github.com/2963609262/ykt-reminder/internal/scheduler"
	"github.com/2963609262/ykt-reminder/internal/store"
	"github.com/2963609262/ykt-reminder/internal/ykt"
)

// Version 由构建时注入（-ldflags "-X ..."）。
var Version = "dev"

// App 持有各层依赖，供所有子命令复用。
type App struct {
	Cfg    config.Config
	Repo   *store.SQLite
	Auth   *auth.Store
	Client *ykt.Client
	Notify *notify.Registry
	Log    *slog.Logger
	Out    io.Writer
}

// Build 构造 App（不启动循环）。
func Build(cfg config.Config, out io.Writer) (*App, error) {
	logger, err := newLogger(cfg.Log)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = os.Stdout
	}

	if err := cfg.EnsureDataDir(); err != nil {
		return nil, err
	}

	repo, err := store.Open(filepath.Join(cfg.DataDir, "state.db"))
	if err != nil {
		return nil, err
	}

	authStore, err := auth.NewStore(filepath.Join(cfg.DataDir, "cookie.json"))
	if err != nil {
		repo.Close()
		return nil, err
	}

	client, err := ykt.New(ykt.Options{
		BaseURL:        cfg.Ykt.BaseURL,
		Identity:       cfg.Ykt.Identity,
		UserAgent:      cfg.Ykt.UserAgent,
		Timeout:        cfg.Poll.RequestTimeout,
		Concurrency:    cfg.Poll.Concurrency,
		ChapterTTL:     cfg.Ykt.ChapterTTL,
		DetailTTL:      cfg.Ykt.DetailTTL,
		DetailTTLTiers: toCacheTiers(cfg.Ykt.DetailTTLTiers),
		MinRequestGap:  cfg.Poll.MinRequestGap,
		Session:        authStore,
		Logger:         logger,
	})
	if err != nil {
		repo.Close()
		return nil, err
	}

	registry, warns, err := notify.NewRegistry(cfg.Notify.Enabled, map[string]func() (notify.Notifier, error){
		"feishu": func() (notify.Notifier, error) {
			return notify.NewFeishu(cfg.Notify.Feishu.Webhook, cfg.Notify.Feishu.Secret, cfg.Notify.Feishu.AtUserID)
		},
		"desktop": func() (notify.Notifier, error) {
			// 桌面通知无需凭据，始终可用。
			return notify.NewDesktop(cfg.Notify.Desktop.AppName), nil
		},
		"webhook": func() (notify.Notifier, error) {
			return notify.NewWebhook(notify.WebhookOptions{
				Name:       cfg.Notify.Webhook.Name,
				URL:        cfg.Notify.Webhook.URL,
				Format:     cfg.Notify.Webhook.Format,
				TitleField: cfg.Notify.Webhook.TitleField,
				BodyField:  cfg.Notify.Webhook.BodyField,
				Headers:    cfg.Notify.Webhook.Headers,
			})
		},
		"email": func() (notify.Notifier, error) {
			return notify.NewEmail(notify.EmailOptions{
				Host:               cfg.Notify.Email.Host,
				Port:               cfg.Notify.Email.Port,
				Username:           cfg.Notify.Email.Username,
				Password:           cfg.Notify.Email.Password,
				From:               cfg.Notify.Email.From,
				To:                 cfg.Notify.Email.To,
				SubjectPrefix:      cfg.Notify.Email.SubjectPrefix,
				InsecureSkipVerify: cfg.Notify.Email.InsecureSkipVerify,
			})
		},
	})
	if err != nil {
		repo.Close()
		return nil, err
	}
	for _, w := range warns {
		logger.Warn(w)
	}

	return &App{
		Cfg:    cfg,
		Repo:   repo,
		Auth:   authStore,
		Client: client,
		Notify: registry,
		Log:    logger,
		Out:    out,
	}, nil
}

// Close 释放资源。
func (a *App) Close() {
	// 先把内存里缓存的登录态回写落盘（ObserveCookie 有 30 秒节流），
	// 否则刚拿到的 csrftoken 会随进程退出一起丢掉。
	if a.Auth != nil {
		if err := a.Auth.Flush(); err != nil {
			a.Log.Warn("登录态回写落盘失败", "err", err)
		}
	}
	if a.Repo != nil {
		_ = a.Repo.Close()
	}
}

// newLogger 构造日志器：文本格式、脱敏友好、可选落盘。
func newLogger(cfg config.LogConfig) (*slog.Logger, error) {
	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(cfg.Level)) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	var w io.Writer = os.Stderr
	if cfg.File != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.File), 0o700); err != nil {
			return nil, fmt.Errorf("创建日志目录失败: %w", err)
		}
		f, err := os.OpenFile(cfg.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, fmt.Errorf("打开日志文件失败: %w", err)
		}
		w = f
	}

	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(h), nil
}

// domainPolicy 把配置转成领域策略。
func domainPolicy(c config.PolicyConfig) domain.PolicyConfig {
	base := domain.DefaultPolicyConfig()
	if len(c.MilestonesBeforeMinutes) > 0 {
		base.Milestones.BeforeDeadline = c.MilestonesBeforeMinutes
	}
	if c.ClassLeadMinutes > 0 {
		base.Milestones.ClassLeadMinutes = c.ClassLeadMinutes
	}
	base.Milestones.NotifyExpired = c.NotifyExpired
	if c.QuietStartHour > 0 || c.QuietEndHour > 0 {
		base.QuietStartHour = c.QuietStartHour
		base.QuietEndHour = c.QuietEndHour
	}
	if c.QuietDeferHour > 0 || c.QuietDeferMinute > 0 {
		base.QuietDeferHour = c.QuietDeferHour
		base.QuietDeferMinute = c.QuietDeferMinute
	}
	if c.UrgentMinutes > 0 {
		base.UrgentMinutes = c.UrgentMinutes
	}
	base.Coalesce = c.Coalesce
	if c.MaxPerCycle > 0 {
		base.MaxPerCycle = c.MaxPerCycle
	}
	return base
}

// NewScheduler 用 App 的依赖构造调度器。
func (a *App) NewScheduler(dryRun bool) (*scheduler.Scheduler, error) {
	p := domainPolicy(a.Cfg.Policy)
	return scheduler.New(scheduler.Options{
		Interval:        a.Cfg.Poll.Interval,
		Jitter:          a.Cfg.Poll.Jitter,
		IdleInterval:    a.Cfg.Poll.IdleInterval,
		IntervalTiers:   toSchedulerTiers(a.Cfg.Poll.IntervalTiers),
		RelaxedInterval: a.Cfg.Poll.RelaxedInterval,
		RelaxHours:      a.Cfg.Poll.RelaxHours,
		ActiveStart:     a.Cfg.Poll.ActiveStartHour,
		ActiveEnd:       a.Cfg.Poll.ActiveEndHour,
		DetailDays:      a.Cfg.Ykt.DetailLookaheadDays,
		Policy:          p,
		WebUIAddr:       a.Cfg.WebUI.Listen,
		Logger:          a.Log,
		Store:           a.Repo,
		Notify:          a.Notify,
		Source:          &scheduler.YktSource{Client: a.Client, DetailDays: a.Cfg.Ykt.DetailLookaheadDays, Logger: a.Log},
		DryRun:          dryRun,
	})
}

// Run 是程序入口：解析子命令并执行。
//
// 支持 --config 出现在子命令前后两种写法：
//
//	ykt-reminder --config x.yaml tasks
//	ykt-reminder tasks --config x.yaml
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		usage(stderr)
		return 2
	}

	// 先从全局参数里剥掉 --config，剩下的第一个非 flag 参数才是子命令。
	configPath, globalArgs := extractConfigFlag(args[1:])
	if len(globalArgs) == 0 {
		usage(stderr)
		return 2
	}
	cmd := globalArgs[0]
	rest := globalArgs[1:]

	// 子命令自身也可能带 --config，再提取一次。
	if p, subArgs := extractConfigFlag(rest); p != "" {
		configPath = p
		rest = subArgs
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "配置错误: %v\n", err)
		return 1
	}

	ctx := context.Background()

	switch cmd {
	case "run":
		return cmdRun(ctx, cfg, rest, stdout, stderr)
	case "tasks":
		return cmdTasks(ctx, cfg, rest, stdout, stderr)
	case "auth":
		return cmdAuth(ctx, cfg, rest, stdout, stderr)
	case "notify":
		return cmdNotify(ctx, cfg, rest, stdout, stderr)
	case "probe":
		return cmdProbe(ctx, cfg, rest, stdout, stderr)
	case "web":
		return cmdWeb(ctx, cfg, rest, stdout, stderr)
	case "version":
		fmt.Fprintf(stdout, "ykt-reminder %s\n", Version)
		return 0
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "未知子命令: %s\n\n", cmd)
		usage(stderr)
		return 2
	}
}

// extractConfigFlag 从参数列表中取出 --config/-config 及其值，
// 返回配置路径与剩余参数。这样顶层不必解析子命令的 flag。
func extractConfigFlag(args []string) (string, []string) {
	var (
		path string
		out  []string
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--config" || a == "-config":
			if i+1 < len(args) {
				path = args[i+1]
				i++
			}
		case len(a) > 9 && (a[:9] == "--config=" || a[:9] == "-config="):
			path = a[9:]
		default:
			out = append(out, a)
		}
	}
	return path, out
}

func usage(w io.Writer) {
	fmt.Fprint(w, `长江雨课堂 · 截止提醒助手

用法:
  ykt-reminder <子命令> [选项]

子命令:
  run       启动常驻提醒服务（默认行为）
  tasks     拉取并列出当前任务（验证数据正确性）
  auth      管理登录态（import / login / status / clear）
  notify    测试通知通道（test）
  probe     只读探查接口字段，输出脱敏快照（排障用）
  web       只启动本地面板（不含定时提醒）
  version   显示版本

通用选项:
  --config <path>   指定配置文件

示例:
  ykt-reminder auth import --cookie "sessionid=xxx; csrftoken=yyy"
  ykt-reminder auth login
  ykt-reminder tasks
  ykt-reminder notify test
  ykt-reminder run --dry-run

配置文件默认位置:
  Windows: %AppData%\ykt-reminder\config.yaml
  Linux/macOS: ~/.config/ykt-reminder/config.yaml
`)
}

// mustBuild 构造 App，失败时打印并返回 nil。
func mustBuild(cfg config.Config, stderr io.Writer) *App {
	app, err := Build(cfg, nil)
	if err != nil {
		fmt.Fprintf(stderr, "初始化失败: %v\n", err)
		return nil
	}
	return app
}

// toSchedulerTiers 把配置里的分级表转成调度器类型。
func toSchedulerTiers(in []config.IntervalTier) []scheduler.IntervalTier {
	if len(in) == 0 {
		return nil
	}
	out := make([]scheduler.IntervalTier, 0, len(in))
	for _, t := range in {
		out = append(out, scheduler.IntervalTier{Within: t.Within, Interval: t.Interval})
	}
	return out
}

// toCacheTiers 把配置里的详情缓存分级转成 ykt 类型。
func toCacheTiers(in []config.DetailTTLTier) []ykt.CacheTier {
	if len(in) == 0 {
		return nil
	}
	out := make([]ykt.CacheTier, 0, len(in))
	for _, t := range in {
		out = append(out, ykt.CacheTier{Within: t.Within, TTL: t.TTL})
	}
	return out
}

// authErrorHint 给登录失效错误附上可操作提示。
func authErrorHint(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ykt.ErrAuthExpired) {
		return fmt.Errorf("%w\n\n提示：请执行 `ykt-reminder auth import --cookie \"...\"`\n或 `ykt-reminder auth login` 扫码登录", err)
	}
	return err
}
