package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	sceneNormalTimeout = 20 * time.Second
	scenePollTimeout   = 75 * time.Second
	scenePollAttempt   = 70*time.Second + 200*time.Millisecond
	sceneQRLifetime    = 5 * time.Minute
	sceneMaxBody       = 2 << 20
)

var scenePathPartRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ErrLoginSessionMissing 表示认证成功响应没有提供 sessionid。
var ErrLoginSessionMissing = errors.New("login_session_missing")

type SceneLogin struct {
	*QRLogin
	pollHTTP  *http.Client
	imageHTTP *http.Client
}

func NewSceneLogin(baseURL string, hc *http.Client) (*SceneLogin, error) {
	if hc == nil {
		return nil, fmt.Errorf("http client 不能为空")
	}

	normal := *hc
	normal.Timeout = sceneNormalTimeout
	normal.CheckRedirect = noSceneRedirect
	flow, err := NewQRLogin(baseURL, &normal)
	if err != nil {
		return nil, err
	}

	pollHTTP := normal
	pollHTTP.Timeout = scenePollTimeout
	imageHTTP := &http.Client{
		Transport:     normal.Transport,
		Timeout:       sceneNormalTimeout,
		CheckRedirect: noSceneRedirect,
	}
	return &SceneLogin{QRLogin: flow, pollHTTP: &pollHTTP, imageHTTP: imageHTTP}, nil
}

func noSceneRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func (q *SceneLogin) Start(ctx context.Context) (QRCode, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, q.baseURL+"/api/v3/user/login/pre-info", nil)
	if err != nil {
		return QRCode{}, err
	}
	req.Header.Set("xtbz", "ykt")
	req.Header.Set("User-Agent", defaultUA)
	req.Header.Set("Referer", q.baseURL+"/authorize/login")

	resp, err := q.http.Do(req)
	if err != nil {
		return QRCode{}, fmt.Errorf("获取场景码失败")
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return QRCode{}, fmt.Errorf("获取场景码返回状态异常")
	}

	body, err := readSceneBody(resp.Body)
	if err != nil {
		return QRCode{}, fmt.Errorf("读取场景码响应失败")
	}
	var envelope struct {
		Code *int `json:"code"`
		Data struct {
			Token     string `json:"token"`
			QRContent string `json:"qrContent"`
			QRImage   string `json:"qrImage"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return QRCode{}, fmt.Errorf("解析场景码响应失败")
	}
	if envelope.Code == nil || *envelope.Code != 0 {
		return QRCode{}, fmt.Errorf("场景码响应未成功")
	}
	if envelope.Data.Token == "" || envelope.Data.QRContent == "" || envelope.Data.QRImage == "" {
		return QRCode{}, fmt.Errorf("场景码响应字段不完整")
	}
	if !validSceneConfirmation(envelope.Data.QRContent) {
		return QRCode{}, fmt.Errorf("场景码链接不合法")
	}
	if !validSceneImage(envelope.Data.QRImage) {
		return QRCode{}, fmt.Errorf("场景码图片链接不合法")
	}

	png, err := q.downloadSceneImage(ctx, envelope.Data.QRImage)
	if err != nil {
		return QRCode{}, err
	}
	return QRCode{
		PNG:             png,
		UUID:            envelope.Data.Token,
		ExpiresAt:       time.Now().Add(sceneQRLifetime),
		confirmationURL: envelope.Data.QRContent,
	}, nil
}

func validSceneConfirmation(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") ||
		!strings.EqualFold(u.Hostname(), "weixin.qq.com") || u.Port() != "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		!strings.HasPrefix(u.Path, "/q/") {
		return false
	}
	return scenePathPartRe.MatchString(strings.TrimPrefix(u.Path, "/q/"))
}

func validSceneImage(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" ||
		!strings.EqualFold(u.Hostname(), "mp.weixin.qq.com") || u.Port() != "" ||
		u.User != nil || u.Path != "/cgi-bin/showqrcode" || u.Fragment != "" {
		return false
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(query) != 1 {
		return false
	}
	tickets := query["ticket"]
	return len(tickets) == 1 && tickets[0] != ""
}

func (q *SceneLogin) downloadSceneImage(ctx context.Context, raw string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, fmt.Errorf("创建场景码图片请求失败")
	}
	req.Header.Set("User-Agent", defaultUA)

	resp, err := q.imageHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载场景码图片失败")
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载场景码图片返回状态异常")
	}
	if !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "image/") {
		return nil, fmt.Errorf("场景码图片响应类型异常")
	}
	body, err := readSceneBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取场景码图片失败")
	}
	return body, nil
}

func readSceneBody(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, sceneMaxBody+1))
	if err != nil {
		return nil, errors.New("读取响应失败")
	}
	if len(body) > sceneMaxBody {
		return nil, fmt.Errorf("响应超过大小限制")
	}
	return body, nil
}

// Poll 每次长轮询超时后使用同一 token 重试，场景码失效时才结束等待。
func (q *SceneLogin) Poll(ctx context.Context, qr QRCode, interval time.Duration, status func(string)) (QRLoginResult, error) {
	if qr.UUID == "" {
		return QRLoginResult{}, fmt.Errorf("场景码令牌缺失")
	}
	waitCtx, cancel := context.WithDeadline(ctx, qr.ExpiresAt)
	defer cancel()

	if interval <= 0 {
		interval = 3 * time.Second
	}

	for {
		if err := sceneSessionError(waitCtx, qr); err != nil {
			return QRLoginResult{}, err
		}
		result, retry, err := q.pollSceneOnce(waitCtx, qr.UUID)
		if err == nil {
			if err := sceneSessionError(waitCtx, qr); err != nil {
				return QRLoginResult{}, err
			}
			return result, nil
		}
		if !retry {
			return QRLoginResult{}, err
		}
		if status != nil {
			status("等待认证…（重试中）")
		}
		if err := waitSceneRetry(waitCtx, qr, interval); err != nil {
			return QRLoginResult{}, err
		}
	}
}

func sceneSessionError(ctx context.Context, qr QRCode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !time.Now().Before(qr.ExpiresAt) {
		return context.DeadlineExceeded
	}
	return nil
}

func waitSceneRetry(ctx context.Context, qr QRCode, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return sceneSessionError(ctx, qr)
	}
}

func (q *SceneLogin) pollSceneOnce(ctx context.Context, token string) (QRLoginResult, bool, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, scenePollAttempt)
	defer cancel()

	payload, err := json.Marshal(struct {
		Token string `json:"token"`
	}{Token: token})
	if err != nil {
		return QRLoginResult{}, false, fmt.Errorf("构造登录轮询请求失败")
	}
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, q.baseURL+"/api/v3/user/login", bytes.NewReader(payload))
	if err != nil {
		return QRLoginResult{}, false, fmt.Errorf("构造登录轮询请求失败")
	}
	req.Header.Set("xtbz", "ykt")
	req.Header.Set("User-Agent", defaultUA)
	req.Header.Set("Referer", q.baseURL+"/authorize/login")
	req.Header.Set("Origin", q.baseURL)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRFToken", q.csrfFromJar())

	resp, err := q.pollHTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return QRLoginResult{}, false, ctx.Err()
		}
		if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
			return QRLoginResult{}, true, context.DeadlineExceeded
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return QRLoginResult{}, true, context.DeadlineExceeded
		}
		return QRLoginResult{}, false, fmt.Errorf("等待场景码认证失败")
	}
	defer drain(resp)

	if ctx.Err() != nil {
		return QRLoginResult{}, false, ctx.Err()
	}
	if resp.StatusCode == http.StatusGatewayTimeout {
		return QRLoginResult{}, false, context.DeadlineExceeded
	}
	if resp.StatusCode != http.StatusOK {
		return QRLoginResult{}, false, fmt.Errorf("场景码认证返回状态异常")
	}

	body, err := readSceneBody(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return QRLoginResult{}, false, ctx.Err()
		}
		if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
			return QRLoginResult{}, true, context.DeadlineExceeded
		}
		return QRLoginResult{}, false, fmt.Errorf("读取场景码认证响应失败")
	}
	if ctx.Err() != nil {
		return QRLoginResult{}, false, ctx.Err()
	}

	var envelope struct {
		Code *int `json:"code"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Code == nil {
		return QRLoginResult{}, false, fmt.Errorf("解析场景码认证响应失败")
	}
	switch *envelope.Code {
	case 0:
		sessionID := q.sessionFromJar()
		if sessionID == "" {
			return QRLoginResult{}, false, ErrLoginSessionMissing
		}
		if ctx.Err() != nil {
			return QRLoginResult{}, false, ctx.Err()
		}
		jar := q.allCookies()
		jar["sessionid"] = sessionID
		return QRLoginResult{
			Cookie: Cookie{
				SessionID: sessionID,
				CSRF:      q.csrfFromJar(),
				Extra:     jar,
				UpdatedAt: time.Now(),
			},
		}, false, nil
	case 500, 50000, 50001:
		return QRLoginResult{}, false, context.DeadlineExceeded
	default:
		return QRLoginResult{}, false, fmt.Errorf("场景码认证未成功")
	}
}
