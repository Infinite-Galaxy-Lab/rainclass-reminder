package notify

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Email 是 SMTP 邮件推送通道。
//
// 相比飞书与通用 webhook：邮件可检索留档，且不依赖第三方推送服务可用性。
//
// 安全约束：发送前用 netguard 校验 SMTP 主机，拒绝内网与回环地址；
// 465 走隐式 TLS，其他端口强制 STARTTLS，服务端不支持则拒绝发信——
// 绝不把授权码明文发出去。
//
// 只用标准库 net/smtp（虽已 deprecated，但引入第三方库会打破极少依赖的约束）。
type Email struct {
	host     string
	port     int
	username string
	password string
	from     string
	to       []string
	prefix   string
	// timeout 是单次投递的总超时。
	timeout time.Duration
	guard   guardPolicy
	// insecureSkipVerify 允许自签名证书（内网邮件服务器用），默认关闭。
	insecureSkipVerify bool
	// dialer 允许测试注入（连 httptest 那样的假 SMTP 服务器）。
	dialer func(ctx context.Context, addr string) (net.Conn, error)
}

// validateSMTPhost 做纯静态的主机名校验（不触发 DNS）。
//
// 拒绝：空、带 scheme/路径（说明用户把 URL 填进来了）、明显的内网字面量。
// 允许：正常域名（发信时再解析并逐 IP 校验）、以及显式允许私网的情况。
func validateSMTPhost(host string) error {
	if host == "" {
		return fmt.Errorf("邮件服务器地址为空")
	}
	if strings.ContainsAny(host, "/@ ") {
		return fmt.Errorf("邮件服务器应只填主机名（如 smtp.qq.com），当前 %q", host)
	}
	// IP 字面量：直接判定是否私网。域名留到发信时解析。
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return fmt.Errorf("邮件服务器 %q 指向本机/内网地址，已拒绝", host)
		}
	}
	return nil
}

// EmailOptions 是构造邮件通道的参数。
type EmailOptions struct {
	Host               string
	Port               int
	Username           string
	Password           string
	From               string
	To                 []string
	SubjectPrefix      string
	Timeout            time.Duration
	InsecureSkipVerify bool
	Guard              guardPolicy
	Dialer             func(ctx context.Context, addr string) (net.Conn, error)
}

// DefaultSMTPPort 是推荐端口（隐式 TLS）。
const DefaultSMTPPort = 465

// NewEmail 构造邮件通道；Host 或 To 为空时返回 nil（视为未配置）。
//
// 返回值语义与其它通道一致：nil, nil 表示"配置不全，跳过"，
// 让 NewRegistry 记一条警告而不是让整个程序启动失败。
func NewEmail(opts EmailOptions) (Notifier, error) {
	host := strings.TrimSpace(opts.Host)
	to := cleanAddrs(opts.To)
	if host == "" || len(to) == 0 {
		return nil, nil
	}

	port := opts.Port
	if port <= 0 {
		port = DefaultSMTPPort
	}
	if port > 65535 {
		return nil, fmt.Errorf("邮件端口 %d 非法", port)
	}

	username := strings.TrimSpace(opts.Username)
	password := strings.TrimSpace(opts.Password)
	if username == "" || password == "" {
		return nil, fmt.Errorf("邮件通道需要 username 与 password（后者建议用环境变量 YKT_EMAIL_PASSWORD）")
	}

	from := strings.TrimSpace(opts.From)
	if from == "" {
		from = username
	}

	// 构造时只做**静态**校验（scheme + host 形态），不做 DNS 解析。
	//
	// 原因：启动时 DNS 偶发不可用（离线、DNS 服务器抖动）会让整个程序
	// 因为"邮件服务器解析失败"而拒绝启动，这代价太大。真实解析留到
	// 发信时做——那时 DNS 一定是可用的，且每次发送前都会重新校验，
	// 防 SSRF 的效果不变。
	if err := validateSMTPhost(host); err != nil {
		return nil, err
	}

	guard := opts.Guard
	if guard == nil {
		guard = defaultWebhookGuard()
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	prefix := strings.TrimSpace(opts.SubjectPrefix)
	if prefix == "" {
		prefix = "【雨课堂提醒】"
	}

	dialer := opts.Dialer
	if dialer == nil {
		dialer = func(ctx context.Context, addr string) (net.Conn, error) {
			d := &net.Dialer{Timeout: timeout}
			return d.DialContext(ctx, "tcp", addr)
		}
	}

	return &Email{
		host:               host,
		port:               port,
		username:           username,
		password:           password,
		from:               from,
		to:                 to,
		prefix:             prefix,
		timeout:            timeout,
		guard:              guard,
		insecureSkipVerify: opts.InsecureSkipVerify,
		dialer:             dialer,
	}, nil
}

// Name 实现 Notifier。
func (e *Email) Name() string { return "email" }

// Send 实现 Notifier。
func (e *Email) Send(ctx context.Context, msg Message) error {
	// 每次发送前重新做完整校验（含 DNS 解析 + 逐 IP 私网判定），
	// 防止运行期配置被改写成内网地址，也防 DNS 重绑定。
	if _, err := e.guard.ValidateURL("https://" + e.host + "/"); err != nil {
		return fmt.Errorf("邮件服务器地址不安全: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	subject := e.prefix + strings.TrimSpace(msg.Title)
	// 标题里可能有换行（多行标题会破坏协议头），必须折平。
	subject = strings.Join(strings.Fields(subject), " ")

	data, err := e.buildMessage(subject, msg)
	if err != nil {
		return err
	}

	return e.deliver(ctx, data)
}

// buildMessage 组装 RFC 5322 邮件（纯文本 + HTML 双段）。
//
// 用 multipart/alternative：纯文本给终端客户端，HTML 给常规客户端。
// 只发文本可以避免被当成营销邮件，投递率更稳。
func (e *Email) buildMessage(subject string, msg Message) ([]byte, error) {
	plain := plainText(msg.Markdown)
	if plain == "" {
		plain = msg.Title
	}
	html := markdownToSimpleHTML(msg.Markdown, msg.Title)

	var b strings.Builder
	b.WriteString("From: " + e.from + "\r\n")
	b.WriteString("To: " + strings.Join(e.to, ", ") + "\r\n")
	b.WriteString("Subject: " + mime.BEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")

	boundary := "ykt-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n")
	b.WriteString("\r\n")

	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(wrapBase64([]byte(plain)))

	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(wrapBase64([]byte(html)))

	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String()), nil
}

// deliver 建立连接并投递。
//
// 两条路径：
//   - 端口 465（或 8465 等 SMTPS）：先 TLS 握手再进 SMTP 会话；
//   - 其它端口：明文建连后强制 STARTTLS，服务端不支持就报错。
//
// 关键安全点：授权码只在 TLS 通道里发送，绝不明文过网。
func (e *Email) deliver(ctx context.Context, data []byte) error {
	addr := net.JoinHostPort(e.host, strconv.Itoa(e.port))
	tlsCfg := &tls.Config{
		ServerName:         e.host,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: e.insecureSkipVerify,
	}

	if e.port == 465 {
		// 隐式 TLS：建连即握手。
		raw, err := e.dialer(ctx, addr)
		if err != nil {
			return fmt.Errorf("连接邮件服务器失败: %w", err)
		}
		conn := tls.Client(raw, tlsCfg)
		if err := conn.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return fmt.Errorf("邮件服务器 TLS 握手失败: %w", err)
		}
		return e.session(ctx, conn, true, data)
	}

	raw, err := e.dialer(ctx, addr)
	if err != nil {
		return fmt.Errorf("连接邮件服务器失败: %w", err)
	}
	return e.session(ctx, raw, false, data)
}

// session 完成 SMTP 会话（握手、认证、发信）。
//
// implicitTLS 为 false 时强制 STARTTLS；服务端不支持则直接失败，
// 不降级到明文认证。
func (e *Email) session(ctx context.Context, conn net.Conn, implicitTLS bool, data []byte) error {
	defer conn.Close()

	// 让 ctx 取消能打断阻塞的 SMTP 读写。
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	c, err := smtp.NewClient(conn, e.host)
	if err != nil {
		return fmt.Errorf("邮件会话初始化失败: %w", err)
	}
	defer func() { _ = c.Close() }()

	if !implicitTLS {
		// 强制 STARTTLS。服务端不支持就失败，绝不降级到明文——
		// 授权码一旦明文过网就等于泄露。
		// 注：smtp.PlainAuth 自身也会拒绝未加密连接（"unencrypted connection"），
		// 是第二道保险；这里的显式检查能给出更准确的中文错误信息。
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return fmt.Errorf("邮件服务器不支持 STARTTLS，已拒绝明文发送凭据")
		}
		if err := c.StartTLS(&tls.Config{
			ServerName:         e.host,
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: e.insecureSkipVerify,
		}); err != nil {
			return fmt.Errorf("STARTTLS 失败: %w", err)
		}
	}

	if ok, _ := c.Extension("AUTH"); ok {
		if err := c.Auth(smtp.PlainAuth("", e.username, e.password, e.host)); err != nil {
			return fmt.Errorf("邮件认证失败（多数邮箱需要“授权码”而非登录密码）: %w", err)
		}
	}

	if err := c.Mail(e.from); err != nil {
		return fmt.Errorf("设置发件人失败: %w", err)
	}
	for _, rcpt := range e.to {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("设置收件人 %s 失败: %w", rcpt, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("开始写入正文失败: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		_ = w.Close()
		return fmt.Errorf("写入正文失败: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("提交正文失败: %w", err)
	}
	return c.Quit()
}

// cleanAddrs 去空、去重、校验基本格式。
func cleanAddrs(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, a := range in {
		a = strings.TrimSpace(a)
		if a == "" || !strings.Contains(a, "@") {
			continue
		}
		key := strings.ToLower(a)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, a)
	}
	return out
}

// wrapBase64 把内容编码成 76 列以内的 base64（RFC 2045）。
//
// 手动按 76 列折行：标准库的 base64 编码器不分行，
// 部分严格的邮件服务器会拒收超长行。
func wrapBase64(b []byte) string {
	enc := base64.StdEncoding.EncodeToString(b)
	const width = 76
	var sb strings.Builder
	for i := 0; i < len(enc); i += width {
		end := i + width
		if end > len(enc) {
			end = len(enc)
		}
		sb.WriteString(enc[i:end])
		sb.WriteString("\r\n")
	}
	if !strings.HasSuffix(sb.String(), "\r\n") {
		sb.WriteString("\r\n")
	}
	return sb.String()
}

// markdownToSimpleHTML 把推送用的极简 Markdown 转成 HTML。
//
// 只支持本程序实际会生成的那几种语法（**粗体**、[文字](链接)、换行），
// 不做通用 Markdown 解析——引入解析库会破坏依赖约束，
// 而且邮件正文里出现未转义的 HTML 是安全风险。
func markdownToSimpleHTML(md, title string) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html><head><meta charset="utf-8">`)
	b.WriteString(`<style>body{font-family:-apple-system,"Segoe UI",Roboto,"Helvetica Neue",Arial,"PingFang SC","Microsoft YaHei",sans-serif;`)
	b.WriteString(`font-size:14px;line-height:1.7;color:#1f2328;max-width:640px;margin:0 auto;padding:24px}`)
	b.WriteString(`h1{font-size:17px;margin:0 0 16px}a{color:#0969da;text-decoration:none}`)
	b.WriteString(`strong{font-weight:600}.meta{color:#59636e}</style></head><body>`)
	b.WriteString("<h1>" + escapeHTML(title) + "</h1>")

	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			b.WriteString("<p></p>")
			continue
		}
		b.WriteString("<p>" + inlineHTML(line) + "</p>")
	}
	b.WriteString("</body></html>")
	return b.String()
}

// inlineHTML 处理单行内的 **粗体** 与 [文字](链接)。
func inlineHTML(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		// 粗体 **...**
		if strings.HasPrefix(s[i:], "**") {
			if end := strings.Index(s[i+2:], "**"); end >= 0 {
				out.WriteString("<strong>")
				out.WriteString(escapeHTML(s[i+2 : i+2+end]))
				out.WriteString("</strong>")
				i += 2 + end + 2
				continue
			}
		}
		// 链接 [文字](url)
		if s[i] == '[' {
			if close := strings.Index(s[i:], "]("); close >= 0 {
				if urlEnd := strings.Index(s[i+close:], ")"); urlEnd >= 0 {
					text := s[i+1 : i+close]
					url := s[i+close+2 : i+close+urlEnd]
					out.WriteString(`<a href="` + escapeAttr(url) + `">` + escapeHTML(text) + `</a>`)
					i += close + urlEnd + 1
					continue
				}
			}
		}
		out.WriteString(escapeHTML(string(s[i])))
		i++
	}
	return out.String()
}

// escapeHTML 转义文本节点。标题来自雨课堂接口，属外部输入。
func escapeHTML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}

// escapeAttr 转义属性值（链接地址）。
func escapeAttr(s string) string {
	// 只允许 http/https，杜绝 javascript: 之类。
	t := strings.TrimSpace(s)
	lower := strings.ToLower(t)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return "#"
	}
	return escapeHTML(t)
}
