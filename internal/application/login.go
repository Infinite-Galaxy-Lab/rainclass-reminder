package application

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/auth"
	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/ykt"
)

const (
	loginTTL          = 5 * time.Minute
	loginPollInterval = 2 * time.Second
)

var (
	errLoginState    = errors.New("login_state")
	errLoginCanceled = errors.New("canceled")
	errLoginExpired  = errors.New("login_expired")
	errLoginFailed   = errors.New("login_failed")
)

// 扫码结果仍须独立校验身份，不能直接持久化。
type Login struct {
	mu sync.Mutex

	ctx      context.Context
	cancel   context.CancelFunc
	flow     loginFlow
	code     auth.QRCode
	interval time.Duration

	startCalled bool
	startReady  bool
	waitCalled  bool
}

type loginFlow interface {
	Start(context.Context) (auth.QRCode, error)
	Poll(context.Context, auth.QRCode, time.Duration, func(string)) (auth.QRLoginResult, error)
}

func NewLogin() (*Login, error) {
	client, err := ykt.New(ykt.Options{
		BaseURL: BaseURL,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		return nil, errLoginFailed
	}
	return newLogin(BaseURL, client.HTTPClient(), loginTTL, loginPollInterval)
}

func newLogin(base string, client *http.Client, ttl, interval time.Duration) (*Login, error) {
	flow, err := auth.NewQRLogin(base, client)
	if err != nil {
		return nil, errLoginFailed
	}
	return loginWithFlow(flow, ttl, interval), nil
}

func loginWithFlow(flow loginFlow, ttl, interval time.Duration) *Login {
	ctx, cancel := context.WithTimeout(context.Background(), ttl)
	return &Login{
		ctx:      ctx,
		cancel:   cancel,
		flow:     flow,
		interval: interval,
	}
}

func (l *Login) Start() ([]byte, error) {
	l.mu.Lock()
	if l.startCalled {
		l.mu.Unlock()
		return nil, errLoginState
	}
	l.startCalled = true
	l.mu.Unlock()

	code, err := l.flow.Start(l.ctx)
	if err != nil {
		result := loginError(l.ctx, err)
		l.cancel()
		return nil, result
	}
	if err := l.ctx.Err(); err != nil {
		result := loginError(l.ctx, err)
		l.cancel()
		return nil, result
	}

	l.mu.Lock()
	l.code = code
	l.startReady = true
	l.mu.Unlock()
	return code.PNG, nil
}

func (l *Login) Wait() (string, error) {
	l.mu.Lock()
	if l.waitCalled {
		l.mu.Unlock()
		return "", errLoginState
	}
	l.waitCalled = true
	if !l.startReady {
		l.mu.Unlock()
		return "", errLoginState
	}
	code := l.code
	l.mu.Unlock()

	defer func() {
		l.cancel()
		l.mu.Lock()
		l.code = auth.QRCode{}
		l.mu.Unlock()
	}()

	result, err := l.flow.Poll(l.ctx, code, l.interval, nil)
	if err != nil {
		return "", loginError(l.ctx, err)
	}
	if err := l.ctx.Err(); err != nil {
		return "", loginError(l.ctx, err)
	}
	req, err := http.NewRequest(http.MethodGet, BaseURL, nil)
	if err != nil {
		return "", errLoginFailed
	}
	result.Cookie.Apply(req)
	return req.Header.Get("Cookie"), nil
}

func (l *Login) Cancel() {
	l.cancel()
}

func loginError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		switch {
		case errors.Is(ctxErr, context.Canceled):
			return errLoginCanceled
		case errors.Is(ctxErr, context.DeadlineExceeded):
			return errLoginExpired
		}
	}
	switch {
	case errors.Is(err, context.Canceled):
		return errLoginCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return errLoginExpired
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return errLoginExpired
	}
	return errLoginFailed
}
