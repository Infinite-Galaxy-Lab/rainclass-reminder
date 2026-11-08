package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/2963609262/ykt-reminder/internal/config"
	"github.com/2963609262/ykt-reminder/internal/scheduler"
	"github.com/2963609262/ykt-reminder/internal/webui"
)

// cmdRun 启动常驻服务。
func cmdRun(ctx context.Context, cfg config.Config, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dryRun := fs.Bool("dry-run", false, "只打印将要发送的内容，不实际推送")
	once := fs.Bool("once", false, "只跑一轮后退出（调试用）")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	app := mustBuild(cfg, stderr)
	if app == nil {
		return 1
	}
	defer app.Close()

	if !app.Auth.Valid() {
		fmt.Fprintln(stderr, "尚未登录。请先执行：")
		fmt.Fprintln(stderr, `  ykt-reminder auth import --cookie "sessionid=...; csrftoken=..."`)
		fmt.Fprintln(stderr, "或：")
		fmt.Fprintln(stderr, "  ykt-reminder auth login")
		return 1
	}

	sch, err := app.NewScheduler(*dryRun)
	if err != nil {
		fmt.Fprintf(stderr, "启动失败: %v\n", err)
		return 1
	}

	// 校验登录态，失效时发一次性告警并退出（避免空转）。
	if err := app.Client.CheckAuth(ctx); err != nil {
		if authErr := authErrorHint(err); authErr != nil {
			fmt.Fprintf(stderr, "登录态校验失败: %v\n", authErr)
		}
		_ = sch.AlertAuthExpired(ctx)
		return 1
	}
	sch.ClearAuthExpired(ctx)

	if *once {
		res := sch.RunCycle(ctx)
		printCycleResult(stdout, res)
		if res.Err != nil {
			return 1
		}
		return 0
	}

	// 优雅退出：Ctrl+C / SIGTERM。
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 可选：同时启动本地面板。
	if app.Cfg.WebUI.Enabled {
		srv, err := webui.New(webui.Options{
			Config:  app.Cfg,
			Client:  app.Client,
			Auth:    app.Auth,
			Repo:    app.Repo,
			Notify:  app.Notify,
			Logger:  app.Log,
			Version: Version,
		})
		if err != nil {
			fmt.Fprintf(stderr, "面板启动失败（提醒服务继续运行）: %v\n", err)
		} else {
			// 把面板的"立即刷新"接到调度器上。
			// 之前两边各有一个缓冲通道但没人消费，按钮点了没有反应。
			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					case <-srv.RefreshC():
						app.Log.Info("面板请求立即刷新")
						sch.Trigger()
					}
				}
			}()
			go func() {
				if err := srv.ListenAndServe(ctx); err != nil && err != context.Canceled {
					app.Log.Warn("面板已停止", "err", err)
				}
			}()
			fmt.Fprintf(stdout, "本地面板：http://%s\n", app.Cfg.WebUI.Listen)
		}
	}

	fmt.Fprintf(stdout, "提醒服务已启动，轮询间隔 %v；按 Ctrl+C 停止。\n", app.Cfg.Poll.Interval)
	err = sch.Run(ctx)
	if err != nil && err != context.Canceled {
		fmt.Fprintf(stderr, "运行结束: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "已停止。")
	return 0
}

// printCycleResult 打印一轮的结果摘要。
func printCycleResult(w io.Writer, res scheduler.CycleResult) {
	fmt.Fprintf(w, "抓取 %d 项，待发 %d 条，成功 %d 条，失败 %d 条，抑制 %d 条，顺延 %d 条\n",
		res.Fetched, res.Due, res.Sent, res.Failed, res.Suppressed, res.Deferred)
	if res.Err != nil {
		fmt.Fprintf(w, "错误: %v\n", res.Err)
	}
}
