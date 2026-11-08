package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/config"
	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/domain"
	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/notify"
	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/ykt"
)

// cmdNotify 测试通知通道。
func cmdNotify(ctx context.Context, cfg config.Config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "test" {
		fmt.Fprintln(stderr, "用法: rainclass-reminder notify test [--dry-run]")
		return 2
	}
	fs := flag.NewFlagSet("notify test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dryRun := fs.Bool("dry-run", false, "只打印消息内容，不实际发送")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	app := mustBuild(cfg, stderr)
	if app == nil {
		return 1
	}
	defer app.Close()

	if app.Notify.Empty() {
		fmt.Fprintln(stderr, "没有启用任何通知通道。")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "可用的通道（在配置 notify.enabled 中启用）：")
		fmt.Fprintln(stderr, "  desktop  桌面系统通知（无需凭据，开箱即用）")
		fmt.Fprintln(stderr, "  feishu   飞书群机器人（需 webhook，环境变量 YKT_FEISHU_WEBHOOK）")
		fmt.Fprintln(stderr, "  webhook  通用 HTTP 推送（Bark/Server酱/企微/钉钉/TG，环境变量 YKT_WEBHOOK_URL）")
		fmt.Fprintln(stderr, "  email    SMTP 邮件（需 host/username/to，环境变量 YKT_EMAIL_PASSWORD）")
		fmt.Fprintln(stderr, "")
		fmt.Fprintf(stderr, "配置文件：%s\n", filepath.Join(config.DefaultConfigDir(), "config.yaml"))
		return 1
	}

	// 构造一条与真实提醒同构的示例，便于验证排版。
	now := time.Now()
	sample := domain.Task{
		ID:       "sample:test",
		Type:     domain.TaskHomework,
		Title:    "这是一条测试提醒（第三章作业）",
		Course:   "高等数学",
		Deadline: now.Add(90 * time.Minute),
		Status:   domain.StatusPending,
		URL:      cfg.Ykt.BaseURL + "/v2/web/index",
	}
	r := domain.Reminder{
		Task: sample,
		Milestone: domain.Milestone{
			TaskID:        sample.ID,
			Kind:          domain.KindBeforeDeadline,
			OffsetMinutes: 60,
			FireAt:        now,
			Deadline:      sample.Deadline,
		},
	}
	msg := notify.RenderReminder(r, time.Now)

	fmt.Fprintf(stdout, "通道: %v\n", app.Notify.Names())
	fmt.Fprintf(stdout, "标题: %s\n", msg.Title)
	fmt.Fprintf(stdout, "内容:\n%s\n", msg.Markdown)

	if *dryRun {
		fmt.Fprintln(stdout, "\n[dry-run] 未实际发送。")
		return 0
	}

	results, err := app.Notify.Send(ctx, msg)
	for _, res := range results {
		if res.Err != nil {
			fmt.Fprintf(stderr, "通道 %s 发送失败: %v\n", res.Channel, res.Err)
		} else {
			fmt.Fprintf(stdout, "通道 %s 发送成功。\n", res.Channel)
		}
	}
	if err != nil {
		return 1
	}
	fmt.Fprintln(stdout, "请检查手机/客户端是否收到测试消息。")
	return 0
}

// cmdProbe 只读探查接口字段，输出脱敏快照，用于接口变更时排障。
func cmdProbe(ctx context.Context, cfg config.Config, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	outDir := fs.String("out", "probe_out", "快照输出目录")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	app := mustBuild(cfg, stderr)
	if app == nil {
		return 1
	}
	defer app.Close()

	if !app.Auth.Valid() {
		fmt.Fprintln(stderr, `尚未登录。请执行：rainclass-reminder auth import --cookie "sessionid=...; csrftoken=..."`)
		return 1
	}

	if err := os.MkdirAll(*outDir, 0o700); err != nil {
		fmt.Fprintf(stderr, "创建输出目录失败: %v\n", err)
		return 1
	}

	info, err := app.Client.BasicInfo(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "登录态无效: %v\n", authErrorHint(err))
		return 1
	}
	fmt.Fprintf(stdout, "用户: %s\n", redactName(info.Nickname.String(), info.Name.String()))

	courses, err := app.Client.Courses(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "课程列表抓取失败: %v\n", authErrorHint(err))
		return 1
	}
	fmt.Fprintf(stdout, "课程数: %d\n", len(courses))

	// 每门课程：章节统计 + 任务列表。
	type courseProbe struct {
		ClassroomID string        `json:"classroom_id"`
		Name        string        `json:"name"`
		LeafCount   int           `json:"leaf_count"`
		LeafTypes   map[int]int   `json:"leaf_types"`
		Tasks       []domain.Task `json:"tasks"`
		Error       string        `json:"error,omitempty"`
	}

	// 全量任务只抓一次，再按 classroom 前缀分组。
	allTasks, stats, fetchErr := app.Client.FetchAll(ctx, cfg.Ykt.DetailLookaheadDays)
	if fetchErr != nil {
		fmt.Fprintf(stderr, "部分抓取失败: %v\n", fetchErr)
	}
	byClassroom := make(map[string][]domain.Task)
	for _, t := range allTasks {
		// ID 形如 ykt:<classroom>:<leaf>
		parts := strings.SplitN(t.ID, ":", 3)
		if len(parts) == 3 {
			byClassroom[parts[1]] = append(byClassroom[parts[1]], t)
		}
	}

	probes := make([]courseProbe, 0, len(courses))
	total := 0
	for _, c := range courses {
		cp := courseProbe{
			ClassroomID: c.ClassroomID.String(),
			Name:        c.DisplayName(),
			LeafTypes:   map[int]int{},
		}
		if chapter, err := app.Client.Chapter(ctx, cp.ClassroomID); err != nil {
			cp.Error = err.Error()
		} else {
			cp.LeafCount = ykt.CountLeaves(chapter)
			cp.LeafTypes = ykt.LeafTypeCounts(chapter)
		}
		cp.Tasks = byClassroom[cp.ClassroomID]
		total += len(cp.Tasks)
		probes = append(probes, cp)
		fmt.Fprintf(stdout, "  - %s：%d 个叶子，类型分布 %v，%d 项任务\n",
			cp.Name, cp.LeafCount, cp.LeafTypes, len(cp.Tasks))
	}

	fmt.Fprintf(stdout, "抓取统计：叶子 %d，拉详情 %d，跳过详情 %d，错误 %d\n",
		stats.Leaves, stats.DetailsFetched, stats.DetailsSkipped, stats.Errors)

	snapshot := map[string]any{
		"generated_at": time.Now().Format(time.RFC3339),
		"base_url":     cfg.Ykt.BaseURL,
		"user":         redactName(info.Nickname.String(), info.Name.String()),
		"course_count": len(courses),
		"task_count":   total,
		"stats":        stats,
		"courses":      probes,
	}

	b, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "序列化快照失败: %v\n", err)
		return 1
	}
	path := filepath.Join(*outDir, fmt.Sprintf("probe_%s.json", time.Now().Format("20060102_150405")))
	if err := os.WriteFile(path, b, 0o600); err != nil {
		fmt.Fprintf(stderr, "写入快照失败: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "\n快照已写入: %s\n", path)
	fmt.Fprintf(stdout, "共 %d 门课程、%d 项任务\n", len(courses), total)
	return 0
}

// redactName 对用户名做轻度脱敏（保留首字）。
func redactName(nick, name string) string {
	s := nick
	if s == "" {
		s = name
	}
	if s == "" {
		return "(未提供)"
	}
	r := []rune(s)
	if len(r) <= 1 {
		return "*"
	}
	return string(r[0]) + "***"
}
