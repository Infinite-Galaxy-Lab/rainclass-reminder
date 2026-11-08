package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/2963609262/ykt-reminder/internal/config"
	"github.com/2963609262/ykt-reminder/internal/domain"
)

// cmdTasks 拉取并打印当前任务，用于人工核对数据准确性。
func cmdTasks(ctx context.Context, cfg config.Config, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tasks", flag.ContinueOnError)
	fs.SetOutput(stderr)
	all := fs.Bool("all", false, "包含已过期任务")
	jsonOut := fs.Bool("json", false, "以 JSON 输出（便于脚本处理）")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	app := mustBuild(cfg, stderr)
	if app == nil {
		return 1
	}
	defer app.Close()

	if !app.Auth.Valid() {
		fmt.Fprintln(stderr, `尚未登录。请执行：ykt-reminder auth import --cookie "sessionid=...; csrftoken=..."`)
		return 1
	}

	tasks, _, err := app.Client.FetchAll(ctx, cfg.Ykt.DetailLookaheadDays)
	if err != nil {
		fmt.Fprintf(stderr, "抓取失败: %v\n", authErrorHint(err))
		// 仍有部分结果时可继续展示
		if len(tasks) == 0 {
			return 1
		}
	}

	now := time.Now()
	filtered := make([]domain.Task, 0, len(tasks))
	for _, t := range tasks {
		if !*all && t.IsExpired(now) && t.Status != domain.StatusPending {
			continue
		}
		filtered = append(filtered, t)
	}
	domain.SortTasks(filtered)

	if *jsonOut {
		printTasksJSON(stdout, filtered, now)
		return 0
	}

	if len(filtered) == 0 {
		fmt.Fprintln(stdout, "没有找到任务。")
		return 0
	}

	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "剩余\t截止\t课程\t类型\t标题\t状态")
	fmt.Fprintln(tw, "----\t----\t----\t----\t----\t----")
	for _, t := range filtered {
		deadline := "—"
		if t.HasDeadline() {
			deadline = t.Deadline.Format("01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			t.Remaining(now), deadline, truncate(t.Course, 12), t.Type.Label(),
			truncate(t.Title, 28), statusLabel(t.Status))
	}
	tw.Flush()
	fmt.Fprintf(stdout, "\n共 %d 项（生成于 %s）\n", len(filtered), now.Format("01-02 15:04:05"))
	return 0
}

// statusLabel 把状态转成中文。
func statusLabel(s domain.TaskStatus) string {
	switch s {
	case domain.StatusDone:
		return "已完成"
	case domain.StatusPending:
		return "未完成"
	case domain.StatusExpired:
		return "已过期"
	default:
		return "未知"
	}
}

// truncate 按 rune 截断，避免中文被截半个字。
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// printTasksJSON 以 JSON 输出任务（手写以免引入依赖，字段与 domain.Task 一致）。
func printTasksJSON(w io.Writer, tasks []domain.Task, now time.Time) {
	fmt.Fprintln(w, "[")
	for i, t := range tasks {
		deadline := ""
		if t.HasDeadline() {
			deadline = t.Deadline.Format(time.RFC3339)
		}
		startAt := ""
		if !t.StartAt.IsZero() {
			startAt = t.StartAt.Format(time.RFC3339)
		}
		fmt.Fprintf(w, `  {"id":%q,"type":%q,"title":%q,"course":%q,"deadline":%q,"start_at":%q,"status":%q,"remaining":%q,"url":%q}`,
			t.ID, t.Type, t.Title, t.Course, deadline, startAt, t.Status, t.Remaining(now), t.URL)
		if i < len(tasks)-1 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "]")
}
