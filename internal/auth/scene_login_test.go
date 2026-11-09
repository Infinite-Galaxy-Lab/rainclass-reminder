package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type sceneTransportFunc func(*http.Request) (*http.Response, error)

func (f sceneTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func sceneTestResponse(r *http.Request, status int, body, contentType string) *http.Response {
	header := make(http.Header)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}
}

func TestQRCodeConfirmationURLUsesSceneLink(t *testing.T) {
	const sceneURL = "https://weixin.qq.com/q/synthetic-code"
	if got := (QRCode{confirmationURL: sceneURL, UUID: "oauth-uuid"}).ConfirmationURL(); got != sceneURL {
		t.Fatalf("ConfirmationURL() = %q, want scene URL", got)
	}
	if got := (QRCode{UUID: "oauth-uuid"}).ConfirmationURL(); got != "https://open.weixin.qq.com/connect/confirm?uuid=oauth-uuid" {
		t.Fatalf("legacy ConfirmationURL() = %q", got)
	}
}

func TestSceneLoginSuccessReusesTokenAndJar(t *testing.T) {
	const (
		base      = "https://ykt.test"
		token     = "synthetic-token"
		sceneURL  = "https://weixin.qq.com/q/synthetic-code"
		imageURL  = "https://mp.weixin.qq.com/cgi-bin/showqrcode?ticket=synthetic-ticket"
		sessionID = "synthetic-session"
	)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(baseURL, []*http.Cookie{{Name: "csrftoken", Value: "synthetic-csrf", Path: "/"}})

	var preInfoCalls, imageCalls, pollCalls int
	transport := sceneTransportFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "ykt.test":
			switch r.URL.Path {
			case "/api/v3/user/login/pre-info":
				preInfoCalls++
				if r.Method != http.MethodGet || r.Header.Get("xtbz") != "ykt" ||
					r.Header.Get("User-Agent") != defaultUA || r.Header.Get("Referer") != base+"/authorize/login" {
					t.Errorf("pre-info request headers/method = %s %#v", r.Method, r.Header)
				}
				if !strings.Contains(r.Header.Get("Cookie"), "csrftoken=synthetic-csrf") {
					t.Errorf("pre-info did not use shared jar: %q", r.Header.Get("Cookie"))
				}
				resp := sceneTestResponse(r, http.StatusOK,
					fmt.Sprintf(`{"code":0,"data":{"token":%q,"qrContent":%q,"qrImage":%q}}`, token, sceneURL, imageURL),
					"application/json")
				resp.Header.Add("Set-Cookie", "preinfo-cookie=available; Path=/")
				return resp, nil
			case "/api/v3/user/login":
				pollCalls++
				if r.Method != http.MethodPost || r.Header.Get("xtbz") != "ykt" ||
					r.Header.Get("Referer") != base+"/authorize/login" || r.Header.Get("Origin") != base ||
					r.Header.Get("Content-Type") != "application/json" ||
					r.Header.Get("X-CSRFToken") != "synthetic-csrf" {
					t.Errorf("poll request headers/method = %s %#v", r.Method, r.Header)
				}
				cookieHeader := r.Header.Get("Cookie")
				if !strings.Contains(cookieHeader, "csrftoken=synthetic-csrf") ||
					!strings.Contains(cookieHeader, "preinfo-cookie=available") {
					t.Errorf("poll did not use shared jar: %q", cookieHeader)
				}
				var payload struct {
					Token string `json:"token"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Errorf("decode poll body: %v", err)
				}
				if payload.Token != token {
					t.Errorf("poll token = %q, want same token", payload.Token)
				}
				resp := sceneTestResponse(r, http.StatusOK, `{"code":0}`, "application/json")
				resp.Header.Add("Set-Cookie", "sessionid="+sessionID+"; Path=/")
				resp.Header.Add("Set-Cookie", "university_id=77; Path=/")
				return resp, nil
			}
		case "mp.weixin.qq.com":
			imageCalls++
			if r.Method != http.MethodGet || r.URL.Path != "/cgi-bin/showqrcode" ||
				r.URL.Query().Get("ticket") != "synthetic-ticket" {
				t.Errorf("image request = %s %s", r.Method, r.URL.Path)
			}
			if r.Header.Get("Cookie") != "" || r.Header.Get("xtbz") != "" || r.Header.Get("Referer") != "" {
				t.Errorf("image request leaked site cookie/headers: %#v", r.Header)
			}
			return sceneTestResponse(r, http.StatusOK, "synthetic-image", "image/png"), nil
		}
		return nil, fmt.Errorf("unexpected request")
	})

	client := &http.Client{Transport: transport, Jar: jar, Timeout: 13 * time.Second}
	flow, err := NewSceneLogin(base, client)
	if err != nil {
		t.Fatal(err)
	}
	if client.Timeout != 13*time.Second || client.CheckRedirect != nil {
		t.Fatal("NewSceneLogin mutated caller client")
	}
	if flow.http.Timeout != sceneNormalTimeout || flow.pollHTTP.Timeout != scenePollTimeout ||
		flow.imageHTTP.Timeout != sceneNormalTimeout {
		t.Fatalf("timeouts ordinary=%s poll=%s image=%s", flow.http.Timeout, flow.pollHTTP.Timeout, flow.imageHTTP.Timeout)
	}
	if flow.imageHTTP.Jar != nil || flow.pollHTTP.Jar != flow.jar || flow.http.Jar != flow.jar {
		t.Fatal("scene clients do not have the required jar isolation")
	}

	started := time.Now()
	code, err := flow.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(code.PNG) != "synthetic-image" || code.UUID != token || code.ConfirmationURL() != sceneURL {
		t.Fatalf("scene code = png:%q uuid:%q confirmation:%q", code.PNG, code.UUID, code.ConfirmationURL())
	}
	if ttl := code.ExpiresAt.Sub(started); ttl < sceneQRLifetime-time.Second || ttl > sceneQRLifetime+time.Second {
		t.Fatalf("scene TTL = %s", ttl)
	}

	result, err := flow.Poll(context.Background(), code, time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Cookie.SessionID != sessionID || result.Cookie.CSRF != "synthetic-csrf" ||
		result.Cookie.Extra["sessionid"] != sessionID || result.Cookie.Extra["university_id"] != "77" ||
		result.Cookie.Extra["preinfo-cookie"] != "available" {
		t.Fatalf("result cookies = %#v", result.Cookie)
	}
	if preInfoCalls != 1 || imageCalls != 1 || pollCalls != 1 {
		t.Fatalf("requests pre-info=%d image=%d poll=%d", preInfoCalls, imageCalls, pollCalls)
	}
}

func TestSceneLoginMissingCodeDoesNotSucceedOrFetchImage(t *testing.T) {
	const token = "synthetic-secret-token"
	const sceneURL = "https://weixin.qq.com/q/synthetic-code"
	const imageURL = "https://mp.weixin.qq.com/cgi-bin/showqrcode?ticket=synthetic-secret-ticket"
	imageCalls := 0
	transport := sceneTransportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "mp.weixin.qq.com" {
			imageCalls++
			t.Fatal("invalid response reached image download")
		}
		return sceneTestResponse(r, http.StatusOK,
			fmt.Sprintf(`{"data":{"token":%q,"qrContent":%q,"qrImage":%q}}`, token, sceneURL, imageURL),
			"application/json"), nil
	})
	flow, err := NewSceneLogin("https://ykt.test", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	code, err := flow.Start(context.Background())
	if err == nil {
		t.Fatal("missing code unexpectedly succeeded")
	}
	if code.UUID != "" || code.ConfirmationURL() != "" || imageCalls != 0 {
		t.Fatalf("missing-code result uuid=%q image_calls=%d", code.UUID, imageCalls)
	}
	for _, secret := range []string{token, sceneURL, imageURL, "synthetic-secret-ticket"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked scene data: %q", err)
		}
	}
}

func TestSceneLoginRejectsInvalidURLsBeforeImageFetch(t *testing.T) {
	validContent := "https://weixin.qq.com/q/synthetic-code"
	validImage := "https://mp.weixin.qq.com/cgi-bin/showqrcode?ticket=synthetic-ticket"
	tests := []struct {
		name      string
		content   string
		image     string
		wantImage bool
	}{
		{"bad-content-host", "https://weixin.qq.com.evil.test/q/code", validImage, false},
		{"bad-content-path", "https://weixin.qq.com/q/code/extra", validImage, false},
		{"content-query", "https://weixin.qq.com/q/code?source=x", validImage, false},
		{"content-fragment", "https://weixin.qq.com/q/code#fragment", validImage, false},
		{"content-port", "https://weixin.qq.com:8443/q/code", validImage, false},
		{"content-user", "https://user@weixin.qq.com/q/code", validImage, false},
		{"content-scheme", "ftp://weixin.qq.com/q/code", validImage, false},
		{"image-http", validContent, "http://mp.weixin.qq.com/cgi-bin/showqrcode?ticket=x", false},
		{"image-host", validContent, "https://mp.weixin.qq.com.evil.test/cgi-bin/showqrcode?ticket=x", false},
		{"image-path", validContent, "https://mp.weixin.qq.com/cgi-bin/other?ticket=x", false},
		{"image-ticket-missing", validContent, "https://mp.weixin.qq.com/cgi-bin/showqrcode", false},
		{"image-ticket-empty", validContent, "https://mp.weixin.qq.com/cgi-bin/showqrcode?ticket=", false},
		{"image-ticket-duplicate", validContent, "https://mp.weixin.qq.com/cgi-bin/showqrcode?ticket=a&ticket=b", false},
		{"image-extra-parameter", validContent, "https://mp.weixin.qq.com/cgi-bin/showqrcode?ticket=a&other=b", false},
		{"image-fragment", validContent, "https://mp.weixin.qq.com/cgi-bin/showqrcode?ticket=x#fragment", false},
		{"image-port", validContent, "https://mp.weixin.qq.com:8443/cgi-bin/showqrcode?ticket=x", false},
		{"image-user", validContent, "https://user@mp.weixin.qq.com/cgi-bin/showqrcode?ticket=x", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imageCalls := 0
			transport := sceneTransportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "mp.weixin.qq.com" {
					imageCalls++
					return sceneTestResponse(r, http.StatusOK, "image", "image/png"), nil
				}
				body, err := json.Marshal(map[string]any{
					"code": 0,
					"data": map[string]any{"token": "synthetic-token", "qrContent": tt.content, "qrImage": tt.image},
				})
				if err != nil {
					t.Fatal(err)
				}
				return sceneTestResponse(r, http.StatusOK, string(body), "application/json"), nil
			})
			flow, err := NewSceneLogin("https://ykt.test", &http.Client{Transport: transport})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := flow.Start(context.Background()); err == nil {
				t.Fatal("invalid URL unexpectedly succeeded")
			}
			if imageCalls != 0 {
				t.Fatalf("invalid URLs caused %d image fetches", imageCalls)
			}
		})
	}
}

func TestSceneLoginRejectsOversizedImage(t *testing.T) {
	content := "https://weixin.qq.com/q/synthetic-code"
	image := "https://mp.weixin.qq.com/cgi-bin/showqrcode?ticket=synthetic-ticket"
	transport := sceneTransportFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "ykt.test":
			body, _ := json.Marshal(map[string]any{
				"code": 0,
				"data": map[string]any{"token": "synthetic-token", "qrContent": content, "qrImage": image},
			})
			return sceneTestResponse(r, http.StatusOK, string(body), "application/json"), nil
		default:
			return sceneTestResponse(r, http.StatusOK, strings.Repeat("x", sceneMaxBody+1), "image/png"), nil
		}
	})
	flow, err := NewSceneLogin("https://ykt.test", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := flow.Start(context.Background()); err == nil {
		t.Fatal("oversized image unexpectedly succeeded")
	}
}

func TestSceneLoginCancelStopsLongPoll(t *testing.T) {
	started := make(chan struct{})
	transport := sceneTransportFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	flow, err := NewSceneLogin("https://ykt.test", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := flow.Poll(ctx, QRCode{UUID: "synthetic-token", ExpiresAt: time.Now().Add(time.Minute)}, time.Millisecond, nil)
		result <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("long poll did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("long poll did not stop after cancellation")
	}
}

func TestSceneLoginPollRetriesAttemptDeadlineWithSameToken(t *testing.T) {
	const token = "synthetic-token"
	polls := 0
	statusCalls := 0
	transport := sceneTransportFunc(func(r *http.Request) (*http.Response, error) {
		polls++
		var payload struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode poll body: %v", err)
		}
		if payload.Token != token {
			t.Fatalf("poll token = %q", payload.Token)
		}
		if polls == 1 {
			return nil, context.DeadlineExceeded
		}
		resp := sceneTestResponse(r, http.StatusOK, `{"code":0}`, "application/json")
		resp.Header.Add("Set-Cookie", "sessionid=synthetic-session; Path=/")
		return resp, nil
	})
	flow, err := NewSceneLogin("https://ykt.test", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	result, err := flow.Poll(context.Background(),
		QRCode{UUID: token, ExpiresAt: time.Now().Add(time.Minute)}, time.Millisecond,
		func(string) { statusCalls++ })
	if err != nil {
		t.Fatal(err)
	}
	if result.Cookie.SessionID != "synthetic-session" || polls != 2 || statusCalls == 0 {
		t.Fatalf("session=%q polls=%d status=%d", result.Cookie.SessionID, polls, statusCalls)
	}
}

func TestSceneLoginPollTimeoutCodesRequireNewCode(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"http-504", http.StatusGatewayTimeout, `{"code":0}`},
		{"code-500", http.StatusOK, `{"code":500}`},
		{"code-50000", http.StatusOK, `{"code":50000}`},
		{"code-50001", http.StatusOK, `{"code":50001}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := sceneTransportFunc(func(r *http.Request) (*http.Response, error) {
				return sceneTestResponse(r, tt.status, tt.body, "application/json"), nil
			})
			flow, err := NewSceneLogin("https://ykt.test", &http.Client{Transport: transport})
			if err != nil {
				t.Fatal(err)
			}
			_, err = flow.Poll(context.Background(),
				QRCode{UUID: "synthetic-token", ExpiresAt: time.Now().Add(time.Minute)}, time.Millisecond, nil)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error = %v, want context.DeadlineExceeded", err)
			}
		})
	}
}

func TestSceneLoginPollMissingSessionUsesSentinel(t *testing.T) {
	transport := sceneTransportFunc(func(r *http.Request) (*http.Response, error) {
		resp := sceneTestResponse(r, http.StatusOK, `{"code":0}`, "application/json")
		resp.Header.Add("Set-Cookie", "set-auth=not-a-cookie; Path=/")
		return resp, nil
	})
	flow, err := NewSceneLogin("https://ykt.test", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	result, err := flow.Poll(context.Background(),
		QRCode{UUID: "synthetic-token", ExpiresAt: time.Now().Add(time.Minute)}, time.Millisecond, nil)
	if !errors.Is(err, ErrLoginSessionMissing) {
		t.Fatalf("error = %v, want ErrLoginSessionMissing", err)
	}
	if result.Cookie.SessionID != "" || err.Error() != "login_session_missing" {
		t.Fatalf("missing session returned cookie=%#v error=%q", result.Cookie, err)
	}
}

func TestSceneLoginPollDoesNotSucceedAfterContextExpiry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	transport := sceneTransportFunc(func(r *http.Request) (*http.Response, error) {
		cancel()
		resp := sceneTestResponse(r, http.StatusOK, `{"code":0}`, "application/json")
		resp.Header.Add("Set-Cookie", "sessionid=must-not-be-returned; Path=/")
		return resp, nil
	})
	flow, err := NewSceneLogin("https://ykt.test", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	result, err := flow.Poll(ctx,
		QRCode{UUID: "synthetic-token", ExpiresAt: time.Now().Add(time.Minute)}, time.Millisecond, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expired context error = %v", err)
	}
	if result.Cookie.SessionID != "" {
		t.Fatalf("expired context returned session %q", result.Cookie.SessionID)
	}
}

func TestNewSceneLoginLeavesNilCallerJarUntouched(t *testing.T) {
	client := &http.Client{Transport: sceneTransportFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("unused")
	})}
	flow, err := NewSceneLogin("https://ykt.test", client)
	if err != nil {
		t.Fatal(err)
	}
	if client.Jar != nil {
		t.Fatal("caller jar was mutated")
	}
	if flow.jar == nil || flow.pollHTTP.Jar != flow.jar || flow.http.Jar != flow.jar {
		t.Fatal("scene flow did not create and reuse its own jar")
	}
}

type sceneExpiringJar struct {
	mu          sync.Mutex
	expires     time.Time
	postSession int
	values      map[string]string
}

func (j *sceneExpiringJar) SetCookies(_ *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.values == nil {
		j.values = make(map[string]string)
	}
	for _, cookie := range cookies {
		j.values[cookie.Name] = cookie.Value
	}
}

func (j *sceneExpiringJar) Cookies(*url.URL) []*http.Cookie {
	j.mu.Lock()
	trigger := false
	if _, ok := j.values["sessionid"]; ok {
		j.postSession++
		trigger = j.postSession == 2
	}
	out := make([]*http.Cookie, 0, len(j.values))
	for name, value := range j.values {
		out = append(out, &http.Cookie{Name: name, Value: value, Path: "/"})
	}
	j.mu.Unlock()
	if trigger {
		time.Sleep(time.Until(j.expires) + 10*time.Millisecond)
	}
	return out
}

func TestSceneLoginPollRejectsMissingTokenWithoutRequest(t *testing.T) {
	calls := 0
	transport := sceneTransportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return sceneTestResponse(r, http.StatusOK, `{"code":0}`, "application/json"), nil
	})
	flow, err := NewSceneLogin("https://ykt.test", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	_, err = flow.Poll(context.Background(), QRCode{ExpiresAt: time.Now().Add(time.Minute)}, time.Millisecond, nil)
	if err == nil {
		t.Fatal("empty token unexpectedly succeeded")
	}
	if calls != 0 {
		t.Fatalf("empty token made %d requests", calls)
	}
}

func TestSceneLoginPollRejectsExpiredQRWithoutRequest(t *testing.T) {
	calls := 0
	transport := sceneTransportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return sceneTestResponse(r, http.StatusOK, `{"code":0}`, "application/json"), nil
	})
	flow, err := NewSceneLogin("https://ykt.test", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	_, err = flow.Poll(context.Background(),
		QRCode{UUID: "synthetic-token", ExpiresAt: time.Now().Add(-time.Second)}, time.Millisecond, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired QR error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("expired QR made %d requests", calls)
	}
}

func TestSceneLoginPollCapsRequestAtQRDeadline(t *testing.T) {
	expires := time.Now().Add(30 * time.Millisecond)
	transport := sceneTransportFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	flow, err := NewSceneLogin("https://ykt.test", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := flow.Poll(context.Background(),
			QRCode{UUID: "synthetic-token", ExpiresAt: expires}, time.Millisecond, nil)
		result <- err
	}()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("QR deadline error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("polling did not stop at the QR deadline")
	}
}

func TestSceneLoginPollChecksQRDeadlineBeforeSuccess(t *testing.T) {
	expires := time.Now().Add(30 * time.Millisecond)
	jar := &sceneExpiringJar{expires: expires}
	transport := sceneTransportFunc(func(r *http.Request) (*http.Response, error) {
		resp := sceneTestResponse(r, http.StatusOK, `{"code":0}`, "application/json")
		resp.Header.Add("Set-Cookie", "sessionid=must-not-be-returned; Path=/")
		return resp, nil
	})
	flow, err := NewSceneLogin("https://ykt.test", &http.Client{Transport: transport, Jar: jar})
	if err != nil {
		t.Fatal(err)
	}
	result, err := flow.Poll(context.Background(),
		QRCode{UUID: "synthetic-token", ExpiresAt: expires}, time.Millisecond, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("QR deadline before success error = %v", err)
	}
	if result.Cookie.SessionID != "" {
		t.Fatalf("QR deadline returned session %q", result.Cookie.SessionID)
	}
}
