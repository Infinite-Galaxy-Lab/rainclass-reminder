// Package config 负责配置加载、校验与跨平台路径约定。
//
// 配置来源优先级（高 → 低）：
//  1. 命令行 --config 指定的文件；
//  2. 环境变量 YKT_CONFIG；
//  3. 平台默认路径下的 config.yaml。
//
// 配置文件与运行数据（cookie、数据库）分目录存放：
// 配置放在用户配置目录，可被版本库跟踪的示例见 configs/config.example.yaml。
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是完整配置树。所有字段都有合理默认值，最小配置只需填 webhook。
type Config struct {
	// DataDir 是运行数据目录（cookie、SQLite）。留空则用平台默认值。
	DataDir string `yaml:"data_dir"`

	Poll   PollConfig   `yaml:"poll"`
	Ykt    YktConfig    `yaml:"ykt"`
	Notify NotifyConfig `yaml:"notify"`
	Policy PolicyConfig `yaml:"policy"`
	WebUI  WebUIConfig  `yaml:"web_ui"`
	Log    LogConfig    `yaml:"log"`

	// sessionCookie 仅用于环境变量注入的初始登录态，不写回配置文件。
	sessionCookie string
}

// PollConfig 控制轮询节奏。
type PollConfig struct {
	// Interval 是正常轮询间隔，默认 10m。
	Interval time.Duration `yaml:"interval"`
	// Jitter 是每轮附加的随机抖动上限，避免整点撞车，默认 60s。
	Jitter time.Duration `yaml:"jitter"`
	// ActiveStartHour / ActiveEndHour 限定活跃时段（本地时间），其余时段拉长间隔。
	ActiveStartHour int `yaml:"active_start_hour"`
	ActiveEndHour   int `yaml:"active_end_hour"`
	// IdleInterval 是非活跃时段的轮询间隔，默认 60m。
	IdleInterval time.Duration `yaml:"idle_interval"`
	// RequestTimeout 是单个 HTTP 请求超时，默认 20s。
	RequestTimeout time.Duration `yaml:"request_timeout"`
	// Concurrency 是课程并发抓取数，默认 4。
	//
	// 注意：并发数只决定"最多几个在飞"，实际发请求速率由 MinRequestGap 控制。
	Concurrency int `yaml:"concurrency"`
	// MinRequestGap 是任意两个请求的最小间隔（全局限速），默认 300ms。
	//
	// 作用：把"瞬间 4 个并发"摊平成"每 300ms 一个"，避免对端网关看到尖峰。
	// 设为 0 表示不限速（不建议）。
	MinRequestGap time.Duration `yaml:"min_request_gap"`
	// RelaxedInterval 是"近期无到期任务"时的放宽间隔，默认 30m。
	// 当最近一个截止时间还在 RelaxHours 之外，用这个间隔代替 Interval。
	RelaxedInterval time.Duration `yaml:"relaxed_interval"`
	// RelaxHours 触发放宽的阈值（小时），默认 6。
	RelaxHours int `yaml:"relax_hours"`
	// IntervalTiers 按"最近截止还剩多久"分级调整轮询间隔，默认四档。
	//
	// 这是最有效的降频手段：距离下一个截止还有 40 小时时，每 20 分钟扫一次
	// 和每 3 分钟扫一次，提醒效果没有区别，但请求量差 6 倍。
	// Within 为 0 的档位视为"兜底档"（永远命中）。
	IntervalTiers []IntervalTier `yaml:"interval_tiers"`
	// RunOnce 为 true 时只跑一轮（调试用）。
	RunOnce bool `yaml:"run_once"`
}

// IntervalTier 是"距离最近截止 ≤ Within 时，用 Interval 轮询"的一档。
type IntervalTier struct {
	Within   time.Duration `yaml:"within"`
	Interval time.Duration `yaml:"interval"`
}

// UnmarshalYAML 宽容解析时长字段。
//
// time.Duration 只接受 "30m" 这类带单位字符串，而兜底档天然要写
// `within: 0`（裸数字），直接反序列化会让配置加载失败。
func (t *IntervalTier) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		Within   flexDuration `yaml:"within"`
		Interval flexDuration `yaml:"interval"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	t.Within = time.Duration(raw.Within)
	t.Interval = time.Duration(raw.Interval)
	return nil
}

// flexDuration 同时接受 "30m"（带单位字符串）、"30"（按秒）、0 三种写法。
type flexDuration time.Duration

// UnmarshalYAML 实现 yaml.Unmarshaler。
func (d *flexDuration) UnmarshalYAML(value *yaml.Node) error {
	if value == nil {
		*d = 0
		return nil
	}
	s := strings.TrimSpace(value.Value)
	if s == "" || s == "~" || s == "null" {
		*d = 0
		return nil
	}
	// 先按带单位解析；失败再试裸数字（按秒）。
	if v, err := time.ParseDuration(s); err == nil {
		*d = flexDuration(v)
		return nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		*d = flexDuration(time.Duration(n) * time.Second)
		return nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		*d = flexDuration(time.Duration(f * float64(time.Second)))
		return nil
	}
	return fmt.Errorf("无法解析时长 %q（应形如 30m / 2h / 0）", s)
}

// YktConfig 是雨课堂站点相关配置。
type YktConfig struct {
	// BaseURL 允许切换到其它雨课堂实例（如荷塘/黄河），默认长江。
	BaseURL string `yaml:"base_url"`
	// Identity 是 courses/list 的 identity 参数，学生为 2。
	Identity int `yaml:"identity"`
	// DetailLookaheadDays 只对这么多天内到期的叶子拉详情，控制请求量。
	DetailLookaheadDays int `yaml:"detail_lookahead_days"`
	// ChapterTTL 是章节树的缓存时长。
	ChapterTTL time.Duration `yaml:"chapter_ttl"`
	// DetailTTL 是叶子详情（leaf_info）的缓存时长，默认 60m。
	//
	// 详情接口是请求量最大的一环。截止时间与完成状态在短时间内变化极小，
	// 缓存后请求量可降 80% 以上。
	//
	// 实际 TTL 会按距离截止还剩多久动态缩短（见 DetailTTLTiers）：
	// 快要到期的任务刷得勤，远期任务几乎不刷。
	DetailTTL time.Duration `yaml:"detail_ttl"`
	// DetailTTLTiers 按距离截止还剩多久分档设置详情缓存时长，默认三档。
	//
	// 远期任务的截止时间几乎不会变，频繁重拉纯属浪费；
	// 一小时内到期的必须保持新鲜。Within 为 0 的档位视为兜底档。
	DetailTTLTiers []DetailTTLTier `yaml:"detail_ttl_tiers"`
	// UserAgent 覆盖默认 UA（一般不用改）。
	UserAgent string `yaml:"user_agent"`
}

// DetailTTLTier 是"距离截止 ≤ Within 时，详情缓存 TTL 用 TTL"的一档。
type DetailTTLTier struct {
	Within time.Duration `yaml:"within"`
	TTL    time.Duration `yaml:"ttl"`
}

// UnmarshalYAML 宽容解析时长字段（理由同 IntervalTier）。
func (t *DetailTTLTier) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		Within flexDuration `yaml:"within"`
		TTL    flexDuration `yaml:"ttl"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	t.Within = time.Duration(raw.Within)
	t.TTL = time.Duration(raw.TTL)
	return nil
}

// NotifyConfig 选择并配置通知通道。
type NotifyConfig struct {
	// Enabled 是启用的通道名列表，按顺序尝试；任一成功即视为已送达。
	// 内置通道：feishu（飞书群机器人）、desktop（桌面通知）、
	// webhook（通用 HTTP）、email（SMTP 邮件）
	Enabled []string      `yaml:"enabled"`
	Feishu  FeishuConfig  `yaml:"feishu"`
	Desktop DesktopConfig `yaml:"desktop"`
	Webhook WebhookConfig `yaml:"webhook"`
	Email   EmailConfig   `yaml:"email"`
}

// EmailConfig 是 SMTP 邮件通道配置。
//
// 适合"留档"场景：提醒同时进邮箱，事后可检索；也是飞书被企业禁用时的
// 替代方案（国内高校邮箱基本都开放 SMTP）。
//
// 安全：密码必须走环境变量 YKT_EMAIL_PASSWORD，配置文件里留空即可。
// Gmail/QQ/163 等要求的是"授权码"而不是登录密码，见 docs 说明。
type EmailConfig struct {
	// Host 是 SMTP 服务器，如 smtp.qq.com。
	Host string `yaml:"host"`
	// Port 是端口：465 走隐式 TLS（推荐），587 走 STARTTLS，
	// 25 是明文（只在收件服务器同网段时考虑）。
	Port int `yaml:"port"`
	// Username 是登录名（通常是完整邮箱地址）。
	Username string `yaml:"username"`
	// Password 是授权码/应用专用密码。环境变量：YKT_EMAIL_PASSWORD
	Password string `yaml:"password"`
	// From 是发件人地址；留空时等于 Username。
	From string `yaml:"from"`
	// To 是收件人列表，至少一个。
	To []string `yaml:"to"`
	// SubjectPrefix 是标题前缀，默认 "【雨课堂提醒】"。
	SubjectPrefix string `yaml:"subject_prefix"`
	// InsecureSkipVerify 仅用于自签名证书的内网邮件服务器，
	// 默认 false。打开前请确认你清楚风险。
	InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
}

// DesktopConfig 是桌面系统通知配置（无需任何凭据）。
type DesktopConfig struct {
	// AppName 显示在通知标题前。
	AppName string `yaml:"app_name"`
}

// WebhookConfig 是通用 HTTP 推送配置。
//
// 用于对接飞书以外的服务（Bark/Server酱/PushPlus/企业微信/钉钉/
// Discord/Telegram 等）。URL 含凭据，必须用环境变量注入。
type WebhookConfig struct {
	// Name 是通道标识，默认 webhook。
	Name string `yaml:"name"`
	// URL 是目标地址，必须 https。环境变量：YKT_WEBHOOK_URL
	URL string `yaml:"url"`
	// Format 取 json（默认）或 text。
	Format string `yaml:"format"`
	// TitleField / BodyField 自定义 JSON 字段名（默认 title / body）。
	TitleField string `yaml:"title_field"`
	BodyField  string `yaml:"body_field"`
	// Headers 是附加请求头（如某些服务需要 Authorization）。
	// 注意：值可能含凭据，建议用环境变量注入敏感头。
	Headers map[string]string `yaml:"headers"`
}

// FeishuConfig 是飞书自定义机器人配置。
type FeishuConfig struct {
	Webhook string `yaml:"webhook"`
	// Secret 用于签名校验（机器人开启"签名校验"时必填）。
	Secret string `yaml:"secret"`
	// AtUserID 可选，@某人（open_id 或 "all"）。
	AtUserID string `yaml:"at_user_id"`
}

// PolicyConfig 是提醒策略配置（对应 domain.PolicyConfig）。
type PolicyConfig struct {
	MilestonesBeforeMinutes []int `yaml:"milestones_before_minutes"`
	ClassLeadMinutes        int   `yaml:"class_lead_minutes"`
	NotifyExpired           bool  `yaml:"notify_expired"`
	QuietStartHour          int   `yaml:"quiet_start_hour"`
	QuietEndHour            int   `yaml:"quiet_end_hour"`
	QuietDeferHour          int   `yaml:"quiet_defer_hour"`
	QuietDeferMinute        int   `yaml:"quiet_defer_minute"`
	UrgentMinutes           int   `yaml:"urgent_minutes"`
	Coalesce                bool  `yaml:"coalesce"`
	MaxPerCycle             int   `yaml:"max_per_cycle"`
}

// WebUIConfig 是本地面板配置。
type WebUIConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
	// Token 非空时，除本机回环外的访问需要带 ?token=。
	Token string `yaml:"token"`
}

// LogConfig 是日志配置。
type LogConfig struct {
	Level string `yaml:"level"` // debug / info / warn / error
	// File 为空则只输出到 stderr。
	File string `yaml:"file"`
}

// Default 返回出厂默认配置。
func Default() Config {
	return Config{
		Poll: PollConfig{
			Interval:        10 * time.Minute,
			Jitter:          60 * time.Second,
			ActiveStartHour: 7,
			ActiveEndHour:   23,
			IdleInterval:    60 * time.Minute,
			RequestTimeout:  20 * time.Second,
			Concurrency:     4,
			MinRequestGap:   300 * time.Millisecond,
			RelaxedInterval: 30 * time.Minute,
			RelaxHours:      6,
			// 按"距最近截止还剩多久"分级：越近扫得越勤。
			IntervalTiers: []IntervalTier{
				{Within: 2 * time.Hour, Interval: 5 * time.Minute},
				{Within: 12 * time.Hour, Interval: 10 * time.Minute},
				{Within: 48 * time.Hour, Interval: 20 * time.Minute},
				{Within: 0, Interval: 30 * time.Minute}, // 兜底：更远
			},
		},
		Ykt: YktConfig{
			BaseURL:             "https://changjiang.yuketang.cn",
			Identity:            2,
			DetailLookaheadDays: 7,
			ChapterTTL:          30 * time.Minute,
			DetailTTL:           60 * time.Minute,
			// 按"距截止还剩多久"分级缓存详情：越近刷得越勤。
			DetailTTLTiers: []DetailTTLTier{
				{Within: 2 * time.Hour, TTL: 15 * time.Minute},
				{Within: 24 * time.Hour, TTL: 60 * time.Minute},
				{Within: 0, TTL: 8 * time.Hour}, // 兜底：更远的任务几乎不刷
			},
			UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		},
		Notify: NotifyConfig{
			Enabled: []string{"feishu"},
			Desktop: DesktopConfig{AppName: "雨课堂提醒"},
			Webhook: WebhookConfig{Name: "webhook", Format: "json"},
		},
		Policy: PolicyConfig{
			MilestonesBeforeMinutes: []int{1440, 360, 60, 15},
			ClassLeadMinutes:        10,
			NotifyExpired:           true,
			QuietStartHour:          23,
			QuietEndHour:            7,
			QuietDeferHour:          7,
			QuietDeferMinute:        30,
			UrgentMinutes:           15,
			Coalesce:                true,
			MaxPerCycle:             8,
		},
		WebUI: WebUIConfig{
			Enabled: true,
			Listen:  "127.0.0.1:8787",
		},
		Log: LogConfig{Level: "info"},
	}
}

// Load 读取配置：先铺默认值，再用文件覆盖，最后用环境变量补关键项。
func Load(path string) (Config, error) {
	cfg := Default()

	path, err := resolvePath(path)
	if err != nil {
		return cfg, err
	}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return cfg, fmt.Errorf("读取配置 %s 失败: %w", path, err)
			}
		} else if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("解析配置 %s 失败: %w", path, err)
		}
	}

	// 环境变量覆盖（便于 Docker / systemd 注入密钥）。
	if v := os.Getenv("YKT_FEISHU_WEBHOOK"); v != "" {
		cfg.Notify.Feishu.Webhook = v
	}
	if v := os.Getenv("YKT_FEISHU_SECRET"); v != "" {
		cfg.Notify.Feishu.Secret = v
	}
	if v := os.Getenv("YKT_WEBHOOK_URL"); v != "" {
		cfg.Notify.Webhook.URL = v
	}
	if v := os.Getenv("YKT_EMAIL_PASSWORD"); v != "" {
		cfg.Notify.Email.Password = v
	}
	if v := os.Getenv("YKT_SESSION_COOKIE"); v != "" {
		// 仅作为初始导入，不写入配置文件。
		cfg.sessionCookie = v
	}
	if v := os.Getenv("YKT_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}

	// 允许 YAML 里写 "10m" 之外的短格式（如 10）。
	cfg.normalizeDurations()
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	if cfg.DataDir == "" {
		cfg.DataDir = DefaultDataDir()
	}
	return cfg, nil
}

// sessionCookie 保存从环境变量传入的 cookie（不参与 yaml 序列化）。
func (c *Config) SessionCookie() string { return c.sessionCookie }

// resolvePath 决定实际要加载的配置文件路径。
func resolvePath(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if v := os.Getenv("YKT_CONFIG"); v != "" {
		return v, nil
	}
	return filepath.Join(DefaultConfigDir(), "config.yaml"), nil
}

// normalizeDurations 处理 YAML 中写成裸数字（秒）的情况。
// time.Duration 的 yaml 反序列化要求带单位，这里兜底做一次修正。
func (c *Config) normalizeDurations() {
	c.Poll.Interval = normalizeDur(c.Poll.Interval, 10*time.Minute)
	c.Poll.Jitter = normalizeDur(c.Poll.Jitter, 60*time.Second)
	c.Poll.IdleInterval = normalizeDur(c.Poll.IdleInterval, 60*time.Minute)
	c.Poll.RequestTimeout = normalizeDur(c.Poll.RequestTimeout, 20*time.Second)
	c.Ykt.ChapterTTL = normalizeDur(c.Ykt.ChapterTTL, 30*time.Minute)
}

func normalizeDur(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// Validate 做配置自检，尽早暴露"看起来能跑但静默失效"的问题。
func (c *Config) Validate() error {
	if c.Poll.Interval < time.Minute {
		return fmt.Errorf("poll.interval 不能小于 1 分钟（当前 %v）", c.Poll.Interval)
	}
	if c.Poll.Concurrency < 1 {
		c.Poll.Concurrency = 1
	}
	if c.Poll.Concurrency > 16 {
		c.Poll.Concurrency = 16
	}
	if !strings.HasPrefix(c.Ykt.BaseURL, "https://") {
		return fmt.Errorf("ykt.base_url 必须是 https 地址（当前 %q）", c.Ykt.BaseURL)
	}
	if c.Ykt.DetailLookaheadDays <= 0 {
		c.Ykt.DetailLookaheadDays = 7
	}
	// 限速默认值：0 视为"未设置"，回落到 300ms，避免配置里漏写就变成不限速。
	if c.Poll.MinRequestGap <= 0 {
		c.Poll.MinRequestGap = 300 * time.Millisecond
	}
	// 上限保护：间隔过大会让一轮抓取拖到几分钟，得不偿失。
	if c.Poll.MinRequestGap > 3*time.Second {
		c.Poll.MinRequestGap = 3 * time.Second
	}
	if c.Ykt.DetailTTL <= 0 {
		c.Ykt.DetailTTL = 60 * time.Minute
	}
	if c.Ykt.ChapterTTL <= 0 {
		c.Ykt.ChapterTTL = 30 * time.Minute
	}
	if c.Poll.RelaxedInterval <= 0 {
		c.Poll.RelaxedInterval = 30 * time.Minute
	}
	// 放宽间隔不应比正常间隔还短，否则"放宽"没意义。
	if c.Poll.RelaxedInterval < c.Poll.Interval {
		c.Poll.RelaxedInterval = c.Poll.Interval
	}
	if c.Poll.RelaxHours <= 0 {
		c.Poll.RelaxHours = 6
	}
	if len(c.Poll.IntervalTiers) == 0 {
		c.Poll.IntervalTiers = Default().Poll.IntervalTiers
	}
	if len(c.Ykt.DetailTTLTiers) == 0 {
		c.Ykt.DetailTTLTiers = Default().Ykt.DetailTTLTiers
	}
	if c.Policy.MilestonesBeforeMinutes == nil {
		c.Policy.MilestonesBeforeMinutes = []int{1440, 360, 60, 15}
	}
	return nil
}

// FeishuReady 判断飞书通道是否可用。
func (c *Config) FeishuReady() bool {
	return strings.TrimSpace(c.Notify.Feishu.Webhook) != ""
}

// DefaultConfigDir 返回平台默认配置目录（config.yaml 直接放在这里）。
func DefaultConfigDir() string {
	return appBaseDir()
}

// DefaultDataDir 返回平台默认数据目录。
func DefaultDataDir() string {
	return filepath.Join(appBaseDir(), "data")
}

const (
	appDirName    = "rainclass-reminder"
	legacyDirName = "ykt-reminder"
)

// appBaseDir 返回程序在用户配置目录下的根目录。
//
// 项目曾用旧名发布，升级用户的数据（登录态与状态库）在新目录里，因此
// 首次运行时把旧目录整体改名为新目录。改名失败（例如旧进程仍持有文件）
// 就继续用旧目录，保证登录态不会因为改名而丢失。
func appBaseDir() string {
	base := osUserConfigDir()
	cur := filepath.Join(base, appDirName)
	old := filepath.Join(base, legacyDirName)

	if _, err := os.Stat(cur); err == nil {
		return cur
	}
	if _, err := os.Stat(old); err != nil {
		return cur // 全新安装
	}
	if err := os.Rename(old, cur); err == nil {
		return cur
	}
	return old // 改名失败：沿用旧目录，数据不丢
}

// osUserConfigDir 在三大平台上返回合理位置；失败时退回家目录。
func osUserConfigDir() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(home, ".config")
}

// EnsureDataDir 创建数据目录并收紧权限（Unix 下 0700）。
func (c *Config) EnsureDataDir() error {
	if err := os.MkdirAll(c.DataDir, 0o700); err != nil {
		return fmt.Errorf("创建数据目录 %s 失败: %w", c.DataDir, err)
	}
	return nil
}

// Redacted 返回可安全打印的配置快照（隐藏密钥）。
func (c Config) Redacted() map[string]any {
	return map[string]any{
		"data_dir": c.DataDir,
		"poll":     fmt.Sprintf("%v (idle %v)", c.Poll.Interval, c.Poll.IdleInterval),
		"ykt":      c.Ykt.BaseURL,
		"notify":   c.Notify.Enabled,
		"feishu":   redactURL(c.Notify.Feishu.Webhook),
		"web_ui":   c.WebUI.Listen,
	}
}

func redactURL(u string) string {
	if u == "" {
		return "(未配置)"
	}
	if i := strings.Index(u, "hook/"); i >= 0 && len(u) > i+5 {
		return u[:i+5] + "***"
	}
	return "(已配置)"
}
