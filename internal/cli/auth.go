package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/2963609262/ykt-reminder/internal/auth"
	"github.com/2963609262/ykt-reminder/internal/config"
	"github.com/2963609262/ykt-reminder/internal/qrutil"
)

// cmdAuth 管理登录态：import / login / status / clear。
func cmdAuth(ctx context.Context, cfg config.Config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, `用法: ykt-reminder auth <import|login|status|clear> [选项]`)
		return 2
	}
	sub := args[0]
	rest := args[1:]

	switch sub {
	case "import":
		return authImport(cfg, rest, stdout, stderr)
	case "login":
		return authLogin(ctx, cfg, rest, stdout, stderr)
	case "status":
		return authStatus(ctx, cfg, stdout, stderr)
	case "clear":
		return authClear(cfg, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "未知的 auth 子命令: %s\n", sub)
		return 2
	}
}

// authImport 从浏览器复制的 Cookie 导入登录态。
func authImport(cfg config.Config, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("auth import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cookie := fs.String("cookie", "", "Cookie 字符串，例如 \"sessionid=xxx; csrftoken=yyy\"")
	file := fs.String("file", "", "从文件读取 Cookie")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	raw := *cookie
	if raw == "" && *file != "" {
		b, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintf(stderr, "读取文件失败: %v\n", err)
			return 1
		}
		raw = string(b)
	}
	if raw == "" {
		fmt.Fprintln(stderr, "请提供 --cookie 或 --file")
		fmt.Fprintln(stderr, "获取方式：浏览器登录雨课堂 → F12 → Application → Cookies → 复制 sessionid 与 csrftoken")
		return 2
	}

	c, err := auth.ParseCookieString(raw)
	if err != nil {
		fmt.Fprintf(stderr, "Cookie 解析失败: %v\n", err)
		return 1
	}

	store, err := auth.NewStore(cookiePath(cfg))
	if err != nil {
		fmt.Fprintf(stderr, "初始化登录态存储失败: %v\n", err)
		return 1
	}
	if err := store.Save(c); err != nil {
		fmt.Fprintf(stderr, "保存登录态失败: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "已保存登录态（%s）\n", c.Redacted())
	fmt.Fprintf(stdout, "配置文件：%s\n", store.Path())
	fmt.Fprintln(stdout, "\n下一步：执行 `ykt-reminder tasks` 验证是否可用。")
	return 0
}

// authLogin 走微信扫码登录。
func authLogin(ctx context.Context, cfg config.Config, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("auth login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	timeout := fs.Duration("timeout", 5*time.Minute, "等待扫码的最长时间")
	noascii := fs.Bool("no-ascii", false, "不打印终端二维码（用于 Web UI 场景）")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	app := mustBuild(cfg, stderr)
	if app == nil {
		return 1
	}
	defer app.Close()

	qr, err := auth.NewQRLogin(cfg.Ykt.BaseURL, app.Client.HTTPClient())
	if err != nil {
		fmt.Fprintf(stderr, "初始化扫码登录失败: %v\n", err)
		return 1
	}

	code, err := qr.Start(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "申请二维码失败: %v\n", err)
		return 1
	}

	// 微信返回 JPEG；统一转 PNG，便于终端渲染与文件保存。
	pngData, err := qrutil.ToPNG(code.PNG)
	if err != nil {
		fmt.Fprintf(stderr, "二维码转码失败: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, "请用微信扫描二维码完成登录：")
	if !*noascii {
		if err := qrutil.PrintQRToTerminal(stdout, pngData); err != nil {
			fmt.Fprintf(stderr, "终端二维码渲染失败（仍可用下面的图片打开扫描）: %v\n", err)
		}
	}
	// 同时保存为文件，便于在没有终端渲染的环境打开。
	imgPath := cookiePath(cfg) + ".qrcode.png"
	if err := os.WriteFile(imgPath, pngData, 0o600); err == nil {
		fmt.Fprintf(stdout, "二维码图片：%s\n", imgPath)
	}

	waitCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	res, err := qr.Poll(waitCtx, code, 2*time.Second, func(status string) {
		fmt.Fprintf(stdout, "\r%s", status)
	})
	fmt.Fprintln(stdout)
	if err != nil {
		fmt.Fprintf(stderr, "登录失败: %v\n", err)
		return 1
	}

	if err := app.Auth.Save(res.Cookie); err != nil {
		fmt.Fprintf(stderr, "保存登录态失败: %v\n", err)
		return 1
	}

	// 校验一次，确认真的能访问接口。
	if info, err := app.Client.BasicInfo(ctx); err == nil {
		name := info.Nickname.String()
		if name == "" {
			name = info.Name.String()
		}
		if name != "" {
			fmt.Fprintf(stdout, "登录成功：%s\n", name)
		} else {
			fmt.Fprintln(stdout, "登录成功。")
		}
		// 回写用户标识，便于 UI 展示。
		c := res.Cookie
		c.UserHint = name
		_ = app.Auth.Save(c)
	} else {
		fmt.Fprintf(stdout, "登录态已保存，但校验失败: %v\n", authErrorHint(err))
	}
	return 0
}

// authStatus 显示当前登录态。
func authStatus(ctx context.Context, cfg config.Config, stdout, stderr io.Writer) int {
	app := mustBuild(cfg, stderr)
	if app == nil {
		return 1
	}
	defer app.Close()

	c := app.Auth.Current()
	if !c.Valid() {
		fmt.Fprintln(stdout, "状态：未登录")
		fmt.Fprintf(stdout, "登录态文件：%s\n", app.Auth.Path())
		return 1
	}

	fmt.Fprintf(stdout, "状态：已保存登录态（%s）\n", c.Redacted())
	if !c.UpdatedAt.IsZero() {
		fmt.Fprintf(stdout, "导入时间：%s\n", c.UpdatedAt.Format("2006-01-02 15:04:05"))
	}
	if c.UserHint != "" {
		fmt.Fprintf(stdout, "用户：%s\n", c.UserHint)
	}

	fmt.Fprint(stdout, "在线校验：")
	info, err := app.Client.BasicInfo(ctx)
	if err != nil {
		fmt.Fprintf(stdout, "失败（%v）\n", authErrorHint(err))
		fmt.Fprintln(stdout, "\n请重新登录：ykt-reminder auth login")
		return 1
	}
	name := info.Nickname.String()
	if name == "" {
		name = info.Name.String()
	}
	fmt.Fprintf(stdout, "有效（用户 %s）\n", name)
	return 0
}

// authClear 清除本地登录态。
func authClear(cfg config.Config, stdout, stderr io.Writer) int {
	store, err := auth.NewStore(cookiePath(cfg))
	if err != nil {
		fmt.Fprintf(stderr, "初始化失败: %v\n", err)
		return 1
	}
	if err := store.Clear(); err != nil {
		fmt.Fprintf(stderr, "清除失败: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "已清除本地登录态。")
	return 0
}

// cookiePath 返回登录态文件路径。
func cookiePath(cfg config.Config) string {
	return cfg.DataDir + string(os.PathSeparator) + "cookie.json"
}
