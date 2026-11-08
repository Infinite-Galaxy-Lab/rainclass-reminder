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
	"github.com/2963609262/ykt-reminder/internal/webui"
)

// cmdWeb 只启动本地面板（不运行定时提醒）。
func cmdWeb(ctx context.Context, cfg config.Config, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", "", "覆盖监听地址，例如 127.0.0.1:8787")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *listen != "" {
		cfg.WebUI.Listen = *listen
	}
	cfg.WebUI.Enabled = true

	app := mustBuild(cfg, stderr)
	if app == nil {
		return 1
	}
	defer app.Close()

	srv, err := webui.New(webui.Options{
		Config:  cfg,
		Client:  app.Client,
		Auth:    app.Auth,
		Repo:    app.Repo,
		Notify:  app.Notify,
		Logger:  app.Log,
		Version: Version,
	})
	if err != nil {
		fmt.Fprintf(stderr, "启动面板失败: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(stdout, "本地面板：http://%s\n", cfg.WebUI.Listen)
	fmt.Fprintln(stdout, "按 Ctrl+C 停止。")
	if err := srv.ListenAndServe(ctx); err != nil && err != context.Canceled {
		fmt.Fprintf(stderr, "面板已停止: %v\n", err)
		return 1
	}
	return 0
}
