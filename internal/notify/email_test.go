package notify

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// 测试用的占位凭据。刻意不用看起来像真实密码的字面量，
// 也不从环境变量读——测试必须能在任何机器上独立跑通。
const (
	testSMTPHost = "smtp.example.com"
	testMailFrom = "sender@example.com"
	testMailTo   = "receiver@example.com"
	// testMailSecret 是纯占位字符串，不对应任何真实账号。
	testMailSecret = "placeholder-not-a-real-secret"
)

// fakeSMTP 是一个最小的 SMTP 服务端，用于在无网络环境下跑通投递流程。
//
// 只实现本通道实际用到的命令（EHLO/AUTH/MAIL/RCPT/DATA/QUIT/STARTTLS 广告）。
// 不实现真实 TLS：因此测试覆盖"服务端通告 STARTTLS"与"不通告"两条分支，
// 前者在本 fake 下会因 TLS 握手失败报错，后者验证安全拒绝。
type fakeSMTP struct {
	ln net.Listener

	mu       sync.Mutex
	data     string
	rcpts    []string
	authUser string
	authPass string
	// advertiseStartTLS 控制是否通告 STARTTLS。
	// 为 false 时，通道应拒绝发送（安全要求：不明文传凭据）。
	advertiseStartTLS bool
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln}
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeSMTP) addr() string { return f.ln.Addr().String() }

func (f *fakeSMTP) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *fakeSMTP) handle(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	write := func(s string) {
		_, _ = w.WriteString(s + "\r\n")
		_ = w.Flush()
	}

	write("220 fake.local ESMTP ready")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb := strings.ToUpper(strings.Fields(line + " x")[0])

		switch verb {
		case "EHLO", "HELO":
			if f.advertiseStartTLS {
				write("250-fake.local")
				write("250-STARTTLS")
				write("250-AUTH PLAIN")
				write("250 OK")
			} else {
				write("250-fake.local")
				write("250-AUTH PLAIN")
				write("250 OK")
			}
		case "AUTH":
			// AUTH PLAIN <base64(\0user\0pass)>
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				raw, decErr := base64.StdEncoding.DecodeString(fields[2])
				if decErr == nil {
					parts := strings.Split(string(raw), "\x00")
					if len(parts) == 3 {
						f.mu.Lock()
						f.authUser, f.authPass = parts[1], parts[2]
						f.mu.Unlock()
					}
				}
			}
			write("235 认证成功")
		case "MAIL":
			write("250 OK")
		case "RCPT":
			if i := strings.Index(line, "<"); i >= 0 {
				if j := strings.Index(line[i:], ">"); j > 0 {
					f.mu.Lock()
					f.rcpts = append(f.rcpts, line[i+1:i+j])
					f.mu.Unlock()
				}
			}
			write("250 OK")
		case "DATA":
			write("354 开始接收")
			var sb strings.Builder
			for {
				l, e := r.ReadString('\n')
				if e != nil {
					return
				}
				if strings.TrimRight(l, "\r\n") == "." {
					break
				}
				sb.WriteString(l)
			}
			f.mu.Lock()
			f.data = sb.String()
			f.mu.Unlock()
			write("250 已接收")
		case "QUIT":
			write("221 bye")
			return
		case "RSET":
			write("250 OK")
		default:
			write("250 OK")
		}
	}
}

func (f *fakeSMTP) snapshot() (data string, rcpts []string, user, pass string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.data, append([]string(nil), f.rcpts...), f.authUser, f.authPass
}

// dialTo 返回连到假服务器的 dialer。
func (f *fakeSMTP) dialTo() func(ctx context.Context, addr string) (net.Conn, error) {
	return func(ctx context.Context, _ string) (net.Conn, error) {
		d := &net.Dialer{Timeout: 3 * time.Second}
		return d.DialContext(ctx, "tcp", f.addr())
	}
}

// TestEmailNotifierName 验证通道标识与构造。
func TestEmailNotifierName(t *testing.T) {
	notifier, err := NewEmail(EmailOptions{
		Host:     testSMTPHost,
		Port:     465,
		Username: testMailFrom,
		Password: testMailSecret,
		To:       []string{testMailTo},
	})
	if err != nil {
		t.Fatal(err)
	}
	if notifier == nil {
		t.Fatal("应返回通道实例")
	}
	if got := notifier.Name(); got != "email" {
		t.Errorf("Name() = %q", got)
	}
}

// noopGuard 跳过 DNS 校验，供离线测试使用。
//
// 生产路径用 defaultWebhookGuard()（会解析并拒绝内网），
// 但测试机可能没有 DNS 或处于离线环境，这里注入一个放行的实现。
type noopGuard struct{}

func (noopGuard) ValidateURL(raw string) (*url.URL, error) { return url.Parse(raw) }

// TestEmailRefusesWithoutSTARTTLS 是安全底线测试。
//
// 服务端不通告 STARTTLS 时必须直接失败：否则授权码会明文过网。
func TestEmailRefusesWithoutSTARTTLS(t *testing.T) {
	f := newFakeSMTP(t)
	f.advertiseStartTLS = false

	notifier, err := NewEmail(EmailOptions{
		Host:     testSMTPHost,
		Port:     2525,
		Username: testMailFrom,
		Password: testMailSecret,
		To:       []string{testMailTo},
		Dialer:   f.dialTo(),
		Guard:    noopGuard{},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = notifier.Send(context.Background(), Message{Title: "测试", Markdown: "正文"})
	if err == nil {
		t.Fatal("服务端不支持 STARTTLS 时应拒绝发送")
	}
	if !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("错误信息应说明 STARTTLS，实际 %v", err)
	}

	// 关键断言：凭据没有泄露出去。
	data, rcpts, user, pass := f.snapshot()
	if user != "" || pass != "" {
		t.Errorf("不应在明文连接上完成认证，却收到了凭据 user=%q pass=%q", user, pass)
	}
	if data != "" || len(rcpts) > 0 {
		t.Errorf("不应投递任何邮件，实际 rcpts=%v data=%q", rcpts, data)
	}
}

// TestEmailBuildMessage 验证邮件结构：双段、base64 编码、标题折平。
func TestEmailBuildMessage(t *testing.T) {
	n, err := NewEmail(EmailOptions{
		Host:     testSMTPHost,
		Username: testMailFrom,
		Password: testMailSecret,
		To:       []string{testMailTo, "cc@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	e := n.(*Email)

	msg := Message{
		Title:    "🚨 习题 即将截止：第三章习题\n第二行",
		Markdown: "**第三章习题**\n课程：概率论\n\n[打开雨课堂](https://changjiang.yuketang.cn/ai-workspace/x)",
	}
	raw, err := e.buildMessage(strings.Join(strings.Fields(msg.Title), " "), msg)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)

	for _, want := range []string{
		"From: " + testMailFrom,
		"To: " + testMailTo + ", cc@example.com",
		"MIME-Version: 1.0",
		"multipart/alternative",
		"text/plain; charset=utf-8",
		"text/html; charset=utf-8",
		"Subject: =?utf-8?", // 中文标题必须编码
	} {
		if !strings.Contains(s, want) {
			t.Errorf("邮件头缺少 %q", want)
		}
	}

	// 标题不能含裸换行（会破坏协议头）。
	for _, line := range strings.Split(s, "\r\n") {
		if strings.HasPrefix(line, "Subject:") && !strings.HasPrefix(line, "Subject: =?utf-8?") {
			t.Errorf("Subject 应整体编码，实际 %q", line)
		}
	}

	// 解出正文，确认内容正确。
	plain := decodeFirstBase64Part(t, s, "text/plain")
	if !strings.Contains(plain, "第三章习题") {
		t.Errorf("纯文本段应含标题正文，实际 %q", plain)
	}
	html := decodeFirstBase64Part(t, s, "text/html")
	if !strings.Contains(html, "<strong>第三章习题</strong>") {
		t.Errorf("HTML 段应把 ** 渲染成 <strong>，实际 %q", html)
	}
	if !strings.Contains(html, `href="https://changjiang.yuketang.cn/`) {
		t.Errorf("HTML 段应保留链接，实际 %q", html)
	}
}

// decodeFirstBase64Part 取出指定 Content-Type 段并解码。
func decodeFirstBase64Part(t *testing.T, raw, contentType string) string {
	t.Helper()
	idx := strings.Index(raw, "Content-Type: "+contentType)
	if idx < 0 {
		t.Fatalf("找不到 %s 段", contentType)
	}
	rest := raw[idx:]
	if i := strings.Index(rest, "\r\n\r\n"); i >= 0 {
		rest = rest[i+4:]
	} else {
		t.Fatalf("%s 段缺少头部结束标记", contentType)
	}
	if end := strings.Index(rest, "--ykt-"); end > 0 {
		rest = rest[:end]
	}
	cleaned := strings.NewReplacer("\r", "", "\n", "").Replace(rest)
	b, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		t.Fatalf("base64 解码失败: %v（内容 %q）", err, cleaned)
	}
	return string(b)
}

// TestEmailHTMLInjectionEscaped 验证任务标题里的 HTML 会被转义。
//
// 标题来自雨课堂接口，属外部输入；邮件正文里未转义即等于注入。
func TestEmailHTMLInjectionEscaped(t *testing.T) {
	html := markdownToSimpleHTML(`<script>alert(1)</script>`, `<img src=x onerror=alert(1)>`)
	if strings.Contains(html, "<script>") {
		t.Error("HTML 段不应包含未转义的 <script>")
	}
	if strings.Contains(html, "<img src=x") {
		t.Error("标题里的标签应被转义")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Error("应转义为 &lt;script&gt;")
	}
}

// TestEmailLinkSchemeFiltered 验证非 http(s) 链接被过滤掉（防 javascript:）。
func TestEmailLinkSchemeFiltered(t *testing.T) {
	got := inlineHTML(`[点我](javascript:alert(1))`)
	if strings.Contains(strings.ToLower(got), "javascript:") {
		t.Errorf("javascript: 链接必须被过滤，实际 %q", got)
	}
	if !strings.Contains(got, `href="#"`) {
		t.Errorf("非法链接应退化为 #，实际 %q", got)
	}
}

// TestEmailConfigValidation 验证配置校验。
func TestEmailConfigValidation(t *testing.T) {
	cases := []struct {
		name    string
		opts    EmailOptions
		wantNil bool
		wantErr bool
	}{
		{
			name:    "未配置主机视为跳过",
			opts:    EmailOptions{},
			wantNil: true,
		},
		{
			name:    "没有收件人视为跳过",
			opts:    EmailOptions{Host: testSMTPHost},
			wantNil: true,
		},
		{
			name: "缺凭据报错",
			opts: EmailOptions{
				Host: testSMTPHost,
				To:   []string{testMailTo},
			},
			wantErr: true,
		},
		{
			name: "端口非法报错",
			opts: EmailOptions{
				Host: testSMTPHost, Port: 70000,
				Username: testMailFrom, Password: testMailSecret,
				To: []string{testMailTo},
			},
			wantErr: true,
		},
		{
			name: "内网主机被拒绝",
			opts: EmailOptions{
				Host:     "127.0.0.1",
				Username: testMailFrom, Password: testMailSecret,
				To: []string{testMailTo},
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, err := NewEmail(tc.opts)
			if tc.wantErr {
				if err == nil {
					t.Error("应报错")
				}
				return
			}
			if err != nil {
				t.Fatalf("不应报错: %v", err)
			}
			if tc.wantNil && n != nil {
				t.Error("应返回 nil 表示未启用")
			}
		})
	}
}

// TestCleanAddrs 验证收件人去重与格式过滤。
func TestCleanAddrs(t *testing.T) {
	got := cleanAddrs([]string{
		"a@x.com", " a@x.com ", "A@X.COM", "", "not-an-email", "b@y.com",
	})
	if len(got) != 2 {
		t.Errorf("应去重并过滤非法项，实际 %v", got)
	}
}

// TestWrapBase64LineLength 验证 base64 折行不超过 76 列。
func TestWrapBase64LineLength(t *testing.T) {
	payload := make([]byte, 500)
	for i := range payload {
		payload[i] = byte('a' + i%26)
	}
	out := wrapBase64(payload)
	for _, line := range strings.Split(strings.TrimRight(out, "\r\n"), "\r\n") {
		if len(line) > 76 {
			t.Errorf("行长度 %d 超过 76：%q", len(line), line)
		}
	}
	if _, err := base64.StdEncoding.DecodeString(
		strings.NewReplacer("\r", "", "\n", "").Replace(out)); err != nil {
		t.Errorf("解码失败: %v", err)
	}
}

// TestEmailRegisteredInRegistry 验证能在注册表中按名字构造。
func TestEmailRegisteredInRegistry(t *testing.T) {
	reg, warns, err := NewRegistry([]string{"email"}, map[string]func() (Notifier, error){
		"email": func() (Notifier, error) {
			return NewEmail(EmailOptions{
				Host:     testSMTPHost,
				Username: testMailFrom,
				Password: testMailSecret,
				To:       []string{testMailTo},
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Errorf("不应有警告: %v", warns)
	}
	if names := reg.Names(); len(names) != 1 || names[0] != "email" {
		t.Errorf("通道名 = %v", names)
	}
}

// TestEmailMessageJSONShape 确保 Message 结构没被改动（防止误改字段）。
func TestEmailMessageJSONShape(t *testing.T) {
	m := Message{Title: "t", Markdown: "m", Urgent: true, Level: "warn"}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"Title":"t"`) {
		t.Errorf("Message 字段名变化会影响其它通道，实际 %s", b)
	}
	_ = io.Discard
}
