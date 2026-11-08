package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// QRLogin 实现微信扫码登录（雨课堂 Web 端 OAuth）。
//
// 流程（与网页端一致）：
//  1. GET  /v/course_meta/user_info        → 让服务端下发 csrftoken
//  2. POST /api/v3/user/login/wechat-auth-param → 拿 appId/state/redirectUri
//  3. GET  open.weixin.qq.com/connect/qrconnect → 取二维码 uid
//  4. GET  open.weixin.qq.com/connect/qrcode/{uid} → 二维码图片
//  5. 轮询 lp.open.weixin.qq.com/connect/l/qrconnect → 拿 wx_code
//  6. GET  /api/v3/user/login/wechat-web-callback → 302 即登录成功，拿到 sessionid
type QRLogin struct {
	baseURL string
	http    *http.Client
	// cookieJar 用 http.CookieJar，保证跨步骤携带 csrftoken。
	jar http.CookieJar
}

// QRCode 是一次登录会话的二维码信息。
type QRCode struct {
	// PNG 是二维码图片字节，可直接显示/保存。
	PNG []byte
	// UUID 是微信轮询标识。
	UUID string
	// State 是 OAuth state（回调时需带回）。
	State string
	// ExpiresAt 是本地估算的过期时间（微信二维码通常 5 分钟）。
	ExpiresAt time.Time
}

// QRLoginResult 是扫码成功后的结果。
type QRLoginResult struct {
	Cookie       Cookie
	RawSetCookie string
}

// NewQRLogin 构造扫码登录器。
func NewQRLogin(baseURL string, hc *http.Client) (*QRLogin, error) {
	if hc == nil {
		return nil, fmt.Errorf("http client 不能为空")
	}
	if hc.Jar == nil {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, err
		}
		hc.Jar = jar
	}
	return &QRLogin{baseURL: strings.TrimRight(baseURL, "/"), http: hc, jar: hc.Jar}, nil
}

// Start 申请二维码，返回可展示的 PNG。
func (q *QRLogin) Start(ctx context.Context) (QRCode, error) {
	// 步骤 1：触发 csrftoken。
	if err := q.warmup(ctx); err != nil {
		return QRCode{}, err
	}

	// 步骤 2：申请 OAuth 参数。
	param, err := q.authParam(ctx)
	if err != nil {
		return QRCode{}, err
	}

	// 步骤 3：从微信二维码页面提取 uid。
	uid, err := q.fetchQRUID(ctx, param)
	if err != nil {
		return QRCode{}, err
	}

	// 步骤 4：下载二维码图片。
	png, err := q.fetchQRImage(ctx, uid)
	if err != nil {
		return QRCode{}, err
	}

	return QRCode{
		PNG:       png,
		UUID:      uid,
		State:     param.State,
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}, nil
}

// authParamData 是 wechat-auth-param 的返回。
type authParamData struct {
	AppID       string `json:"appId"`
	State       string `json:"state"`
	RedirectURI string `json:"redirectUri"`
}

// warmup 访问轻量接口，确保 jar 里有 csrftoken。
func (q *QRLogin) warmup(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, q.baseURL+"/v/course_meta/user_info", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := q.http.Do(req)
	if err != nil {
		return fmt.Errorf("初始化登录会话失败: %w", err)
	}
	defer drain(resp)
	return nil
}

// authParam 申请微信 OAuth 参数。
func (q *QRLogin) authParam(ctx context.Context) (authParamData, error) {
	body := strings.NewReader("{}")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		q.baseURL+"/api/v3/user/login/wechat-auth-param", body)
	if err != nil {
		return authParamData{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c := q.csrfFromJar(); c != "" {
		req.Header.Set("X-CSRFToken", c)
	}

	resp, err := q.http.Do(req)
	if err != nil {
		return authParamData{}, fmt.Errorf("申请登录参数失败: %w", err)
	}
	defer drain(resp)

	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return authParamData{}, err
	}
	var env struct {
		Data authParamData `json:"data"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return authParamData{}, fmt.Errorf("解析登录参数失败（响应：%.200s）", string(b))
	}
	if env.Data.AppID == "" || env.Data.State == "" {
		return authParamData{}, fmt.Errorf("登录参数不完整，接口可能已变更（响应：%.200s）", string(b))
	}
	return env.Data, nil
}

var qrUIDRe = regexp.MustCompile(`/connect/qrcode/([A-Za-z0-9_-]+)`)

// fetchQRUID 打开微信扫码页并提取 uid。
func (q *QRLogin) fetchQRUID(ctx context.Context, p authParamData) (string, error) {
	u := "https://open.weixin.qq.com/connect/qrconnect?" + url.Values{
		"appid":         {p.AppID},
		"scope":         {"snsapi_login"},
		"redirect_uri":  {p.RedirectURI},
		"state":         {p.State},
		"login_type":    {"jssdk"},
		"self_redirect": {"true"},
		"styletype":     {""},
		"sizetype":      {""},
		"bgcolor":       {""},
		"rst":           {""},
		"ts":            {""},
		"stylelite":     {"1"},
	}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", defaultUA)

	resp, err := q.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("打开微信扫码页失败: %w", err)
	}
	defer drain(resp)

	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	m := qrUIDRe.FindSubmatch(b)
	if len(m) < 2 {
		return "", fmt.Errorf("未能从微信页面解析二维码标识，登录流程可能已变更")
	}
	return string(m[1]), nil
}

// fetchQRImage 下载二维码 PNG。
func (q *QRLogin) fetchQRImage(ctx context.Context, uid string) ([]byte, error) {
	u := "https://open.weixin.qq.com/connect/qrcode/" + url.PathEscape(uid)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", defaultUA)

	resp, err := q.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载二维码失败: %w", err)
	}
	defer drain(resp)

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
		return nil, fmt.Errorf("二维码响应类型异常: %s", ct)
	}
	// 实测微信返回 image/jpeg，因此不假定 PNG，按原样保存字节由上层识别格式。
	return io.ReadAll(io.LimitReader(resp.Body, 2<<20))
}

var wxCodeRe = regexp.MustCompile(`window\.wx_code='([^']*)'`)

// Poll 轮询扫码状态，直到成功、过期或 ctx 结束。
//
// 每次返回当前状态供 UI 展示；成功时通过 channel 返回结果。
func (q *QRLogin) Poll(ctx context.Context, qr QRCode, interval time.Duration, status func(string)) (QRLoginResult, error) {
	if interval <= 0 {
		interval = 3 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return QRLoginResult{}, ctx.Err()
		case <-ticker.C:
			if time.Now().After(qr.ExpiresAt) {
				return QRLoginResult{}, fmt.Errorf("二维码已过期，请重新扫码")
			}
			code, err := q.pollOnce(ctx, qr.UUID)
			if err != nil {
				// 单次网络抖动不终止，继续轮询。
				if status != nil {
					status("等待扫码…（网络波动，重试中）")
				}
				continue
			}
			if code == "" {
				if status != nil {
					status("等待扫码…")
				}
				continue
			}
			if code == "405" || code == "404" {
				// 404/405 表示已扫码但未确认，或二维码失效。
				if status != nil {
					status("已扫码，请在手机上确认")
				}
				continue
			}
			if strings.HasPrefix(code, "40") {
				if status != nil {
					status("已扫码，请在手机上确认")
				}
				continue
			}
			// 成功：用 code 换 sessionid。
			return q.callback(ctx, code, qr.State)
		}
	}
}

// pollOnce 查询一次扫码状态。
//
// 微信返回形如 window.wx_code='xxxx'; 的脚本，成功时包含非 408 的 code。
func (q *QRLogin) pollOnce(ctx context.Context, uid string) (code string, err error) {
	u := "https://lp.open.weixin.qq.com/connect/l/qrconnect?uuid=" + url.QueryEscape(uid)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", defaultUA)
	req.Header.Set("Referer", "https://open.weixin.qq.com/")

	resp, err := q.http.Do(req)
	if err != nil {
		return "", err
	}
	defer drain(resp)

	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	m := wxCodeRe.FindSubmatch(b)
	if len(m) < 2 {
		return "", nil
	}
	return string(m[1]), nil
}

// callback 用 wx_code 换 sessionid。
func (q *QRLogin) callback(ctx context.Context, wxCode, state string) (QRLoginResult, error) {
	u := q.baseURL + "/api/v3/user/login/wechat-web-callback?" + url.Values{
		"path":  {"/authorize/wx-qrlogin?success=1"},
		"code":  {wxCode},
		"state": {state},
	}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return QRLoginResult{}, err
	}
	req.Header.Set("User-Agent", defaultUA)

	resp, err := q.http.Do(req)
	if err != nil {
		return QRLoginResult{}, fmt.Errorf("换取登录态失败: %w", err)
	}
	defer drain(resp)

	// 302 表示成功；200 也可能（旧版本）。
	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusOK &&
		resp.StatusCode != http.StatusMovedPermanently {
		return QRLoginResult{}, fmt.Errorf("登录回调返回 %d，登录未成功", resp.StatusCode)
	}

	sessionID := q.sessionFromJar()
	if sessionID == "" {
		// 从 Set-Cookie 里再找一次（某些情况下 jar 未及时更新）。
		for _, sc := range resp.Header.Values("Set-Cookie") {
			if strings.HasPrefix(sc, "sessionid=") {
				if v, _, ok := strings.Cut(strings.TrimPrefix(sc, "sessionid="), ";"); ok {
					sessionID = v
				} else {
					sessionID = strings.TrimPrefix(sc, "sessionid=")
				}
				break
			}
		}
	}
	if sessionID == "" {
		return QRLoginResult{}, fmt.Errorf("登录回调成功但未获取到 sessionid")
	}

	// 把 jar 里的全部 cookie 都存下来，而不只是 sessionid + csrftoken。
	// 网页端登录后还会拿到 university_id、django_language 等辅助 cookie，
	// 少带它们时个别接口会降级（甚至 403）。既然只读，多带无害。
	jar := q.allCookies()
	jar["sessionid"] = sessionID

	return QRLoginResult{
		Cookie: Cookie{
			SessionID: sessionID,
			CSRF:      q.csrfFromJar(),
			Extra:     jar,
			UpdatedAt: time.Now(),
		},
	}, nil
}

// allCookies 返回 jar 中该站点的全部 cookie（键值对）。
func (q *QRLogin) allCookies() map[string]string {
	if q.jar == nil {
		return nil
	}
	u, err := url.Parse(q.baseURL)
	if err != nil {
		return nil
	}
	out := make(map[string]string)
	for _, c := range q.jar.Cookies(u) {
		if c.Name != "" && c.Value != "" {
			out[c.Name] = c.Value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// csrfFromJar 从 cookie jar 里读 csrftoken。
func (q *QRLogin) csrfFromJar() string {
	return q.cookieValue("csrftoken")
}

// sessionFromJar 从 cookie jar 里读 sessionid。
func (q *QRLogin) sessionFromJar() string {
	return q.cookieValue("sessionid")
}

// cookieValue 在 jar 中查找指定 cookie（同时尝试根路径与该站点）。
func (q *QRLogin) cookieValue(name string) string {
	if q.jar == nil {
		return ""
	}
	u, err := url.Parse(q.baseURL)
	if err != nil {
		return ""
	}
	for _, c := range q.jar.Cookies(u) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

const defaultUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// drain 读完并关闭响应体，让连接可复用。
func drain(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
}
