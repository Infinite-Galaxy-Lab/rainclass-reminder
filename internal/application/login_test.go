package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/auth"
	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/ykt"
)

const syntheticLoginBase = "https://ykt.test"

type completedLoginFlow struct {
	cancel context.CancelFunc
	err    error
}

func (f *completedLoginFlow) Start(context.Context) (auth.QRCode, error) {
	return auth.QRCode{UUID: "synthetic", PNG: []byte("image")}, nil
}

func (f *completedLoginFlow) Poll(context.Context, auth.QRCode, time.Duration, func(string)) (auth.QRLoginResult, error) {
	if f.cancel != nil {
		f.cancel()
	}
	return auth.QRLoginResult{Cookie: auth.Cookie{SessionID: "synthetic-session"}}, f.err
}

func TestLoginRejectsResultDeliveredAfterCancellation(t *testing.T) {
	flow := &completedLoginFlow{}
	login := loginWithFlow(flow, time.Minute, time.Millisecond)
	defer login.Cancel()
	flow.cancel = login.Cancel
	if _, err := login.Start(); err != nil {
		t.Fatal(err)
	}
	if cookie, err := login.Wait(); cookie != "" || err != errLoginCanceled {
		t.Fatal("canceled flow delivered a login result")
	}
}

type syntheticLoginTransport struct {
	mu sync.Mutex

	polls             int
	callbacks         int
	requests          int
	missingSession    bool
	callbackCode      string
	callbackState     string
	unexpectedRequest string

	blockPoll bool
	pollOnce  sync.Once
	polled    chan struct{}
}

func (s *syntheticLoginTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	s.requests++
	s.mu.Unlock()

	switch {
	case req.URL.Host == "ykt.test" && req.URL.Path == "/v/course_meta/user_info":
		resp := syntheticResponse(req, http.StatusOK, `{}`, "application/json")
		resp.Header.Add("Set-Cookie", "csrftoken=csrf-456; Path=/")
		return resp, nil

	case req.URL.Host == "ykt.test" && req.URL.Path == "/api/v3/user/login/wechat-auth-param":
		return syntheticResponse(req, http.StatusOK,
			`{"data":{"appId":"wx-app","state":"state-123","redirectUri":"https://ykt.test/callback"}}`,
			"application/json"), nil

	case req.URL.Host == "open.weixin.qq.com" && req.URL.Path == "/connect/qrconnect":
		return syntheticResponse(req, http.StatusOK, `<img src="/connect/qrcode/qr-uid">`, "text/html"), nil

	case req.URL.Host == "open.weixin.qq.com" && req.URL.Path == "/connect/qrcode/qr-uid":
		return syntheticResponse(req, http.StatusOK, "qr-image", "image/jpeg"), nil

	case req.URL.Host == "lp.open.weixin.qq.com" && req.URL.Path == "/connect/l/qrconnect":
		if s.blockPoll {
			s.pollOnce.Do(func() { close(s.polled) })
			<-req.Context().Done()
			return nil, req.Context().Err()
		}
		s.mu.Lock()
		s.polls++
		n := s.polls
		s.mu.Unlock()
		switch n {
		case 1:
			return syntheticResponse(req, http.StatusOK, `<html></html>`, "text/html"), nil
		case 2:
			return syntheticResponse(req, http.StatusOK, `window.wx_code='404';`, "text/html"), nil
		case 3:
			return syntheticResponse(req, http.StatusOK, `window.wx_code='405';`, "text/html"), nil
		case 4:
			return syntheticResponse(req, http.StatusOK, `window.wx_code='408';`, "text/html"), nil
		default:
			return syntheticResponse(req, http.StatusOK, `window.wx_code='wx-success';`, "text/html"), nil
		}

	case req.URL.Host == "ykt.test" && req.URL.Path == "/api/v3/user/login/wechat-web-callback":
		s.mu.Lock()
		s.callbacks++
		s.callbackCode = req.URL.Query().Get("code")
		s.callbackState = req.URL.Query().Get("state")
		s.mu.Unlock()
		resp := syntheticResponse(req, http.StatusFound, `{}`, "application/json")
		resp.Header.Set("Location", "/authorize/wx-qrlogin?success=1")
		if !s.missingSession {
			resp.Header.Add("Set-Cookie", "sessionid=sess-123; Path=/")
			resp.Header.Add("Set-Cookie", "university_id=77; Path=/")
			resp.Header.Add("Set-Cookie", "django_language=zh-cn; Path=/")
		}
		return resp, nil
	}

	s.mu.Lock()
	s.unexpectedRequest = req.Method + " " + req.URL.String()
	s.mu.Unlock()
	return syntheticResponse(req, http.StatusTeapot, `unexpected`, "text/plain"), nil
}

func syntheticResponse(req *http.Request, status int, body, contentType string) *http.Response {
	h := make(http.Header)
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func newSyntheticLogin(t *testing.T, rt http.RoundTripper, ttl, interval time.Duration) *Login {
	t.Helper()
	client, err := ykt.New(ykt.Options{
		BaseURL:       syntheticLoginBase,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Transport:     rt,
		MinRequestGap: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	login, err := newLogin(syntheticLoginBase, client.HTTPClient(), ttl, interval)
	if err != nil {
		t.Fatal(err)
	}
	return login
}

func TestLoginCompletesSyntheticFlowOnce(t *testing.T) {
	rt := &syntheticLoginTransport{}
	login := newSyntheticLogin(t, rt, time.Second, time.Millisecond)

	png, err := login.Start()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(png); got != "qr-image" {
		t.Fatalf("PNG = %q", got)
	}
	rt.mu.Lock()
	requestsAfterStart := rt.requests
	rt.mu.Unlock()
	if _, err := login.Start(); err != errLoginState {
		t.Fatalf("second Start error = %v", err)
	}
	rt.mu.Lock()
	if rt.requests != requestsAfterStart {
		t.Errorf("second Start made network requests: %d -> %d", requestsAfterStart, rt.requests)
	}
	rt.mu.Unlock()

	header, err := login.Wait()
	if err != nil {
		t.Fatal(err)
	}
	wantHeader := "sessionid=sess-123; csrftoken=csrf-456; django_language=zh-cn; university_id=77"
	if header != wantHeader {
		t.Errorf("Cookie header = %q, want %q", header, wantHeader)
	}

	rt.mu.Lock()
	polls, callbacks := rt.polls, rt.callbacks
	code, state, unexpected := rt.callbackCode, rt.callbackState, rt.unexpectedRequest
	rt.mu.Unlock()
	if polls != 5 || callbacks != 1 || code != "wx-success" || state != "state-123" {
		t.Errorf("exchange = polls:%d callbacks:%d code:%q state:%q", polls, callbacks, code, state)
	}
	if unexpected != "" {
		t.Errorf("unexpected request: %s", unexpected)
	}
	if _, err := login.Wait(); err != errLoginState {
		t.Fatalf("second Wait error = %v", err)
	}
}

type blockingStartTransport struct {
	once    sync.Once
	started chan struct{}
}

func (b *blockingStartTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	b.once.Do(func() { close(b.started) })
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func TestLoginCancelAbortsStartWithoutStateLock(t *testing.T) {
	rt := &blockingStartTransport{started: make(chan struct{})}
	login := newSyntheticLogin(t, rt, 2*time.Second, time.Millisecond)
	result := make(chan error, 1)
	go func() {
		_, err := login.Start()
		result <- err
	}()
	<-rt.started

	login.mu.Lock()
	cancelled := make(chan struct{})
	go func() {
		login.Cancel()
		close(cancelled)
	}()
	select {
	case <-cancelled:
	case <-time.After(500 * time.Millisecond):
		login.mu.Unlock()
		t.Fatal("Cancel blocked on state mutex")
	}
	login.mu.Unlock()
	if !errors.Is(login.ctx.Err(), context.Canceled) {
		t.Fatalf("Cancel did not cancel context: %v", login.ctx.Err())
	}

	select {
	case err := <-result:
		if err != errLoginCanceled {
			t.Fatalf("Start error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Start did not exit after Cancel")
	}
}

func TestLoginCancelAbortsWait(t *testing.T) {
	rt := &syntheticLoginTransport{blockPoll: true, polled: make(chan struct{})}
	login := newSyntheticLogin(t, rt, time.Second, time.Millisecond)
	if _, err := login.Start(); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		_, err := login.Wait()
		result <- err
	}()
	select {
	case <-rt.polled:
	case <-time.After(time.Second):
		t.Fatal("Poll was not reached")
	}
	login.Cancel()
	select {
	case err := <-result:
		if err != errLoginCanceled {
			t.Fatalf("Wait error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Wait did not exit after Cancel")
	}
}

func TestLoginWaitExpires(t *testing.T) {
	rt := &syntheticLoginTransport{blockPoll: true, polled: make(chan struct{})}
	login := newSyntheticLogin(t, rt, 100*time.Millisecond, time.Millisecond)
	if _, err := login.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := login.Wait(); err != errLoginExpired {
		t.Fatalf("Wait error = %v", err)
	}
}

func TestLoginRejectsCallbackWithoutSession(t *testing.T) {
	rt := &syntheticLoginTransport{missingSession: true}
	login := newSyntheticLogin(t, rt, time.Second, time.Millisecond)
	if _, err := login.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := login.Wait(); err != errLoginFailed {
		t.Fatalf("Wait error = %v", err)
	}
}

type failingAuthTransport struct{}

func (failingAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path == "/v/course_meta/user_info" {
		resp := syntheticResponse(req, http.StatusOK, `{}`, "application/json")
		resp.Header.Add("Set-Cookie", "csrftoken=csrf-456; Path=/")
		return resp, nil
	}
	return syntheticResponse(req, http.StatusBadGateway,
		`token-SECRET https://private.invalid/response`, "text/plain"), nil
}

func TestLoginHidesForeignErrorsAndCancelsFailedStart(t *testing.T) {
	login := newSyntheticLogin(t, failingAuthTransport{}, time.Second, time.Millisecond)
	if _, err := login.Start(); err != errLoginFailed {
		t.Fatalf("Start error = %v", err)
	}
	if !errors.Is(login.ctx.Err(), context.Canceled) {
		t.Fatalf("failed Start did not cancel context: %v", login.ctx.Err())
	}
	if _, err := login.Start(); err != errLoginState {
		t.Fatalf("Start retry error = %v", err)
	}
}
