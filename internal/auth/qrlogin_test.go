package auth

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type qrTransport func(*http.Request) (*http.Response, error)

func (f qrTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func qrResponse(r *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func TestQRLoginAcknowledgesScannedState(t *testing.T) {
	for _, scanned := range []string{"404", "403"} {
		t.Run(scanned, func(t *testing.T) {
			polls, callbacks := 0, 0
			client := &http.Client{Transport: qrTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "lp.open.weixin.qq.com" {
					polls++
					if polls == 1 || r.URL.Query().Get("last") != scanned {
						return qrResponse(r, "window.wx_errcode="+scanned+";window.wx_code='';"), nil
					}
					return qrResponse(r, "window.wx_errcode=405;window.wx_code='confirmed';"), nil
				}
				callbacks++
				if r.URL.Query().Get("code") != "confirmed" {
					t.Error("incorrect confirmation code")
				}
				response := qrResponse(r, "")
				response.Header.Set("Set-Cookie", "sessionid=synthetic; Path=/")
				return response, nil
			})}
			flow, err := NewQRLogin("https://ykt.test", client)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			result, err := flow.Poll(ctx, QRCode{UUID: "synthetic", State: "synthetic", ExpiresAt: time.Now().Add(time.Minute)}, time.Millisecond, nil)
			if err != nil {
				t.Fatalf("confirmation was lost without scan acknowledgement: %v", err)
			}
			if result.Cookie.SessionID != "synthetic" || polls != 2 || callbacks != 1 {
				t.Fatalf("polls=%d callbacks=%d session_present=%t", polls, callbacks, result.Cookie.SessionID != "")
			}
		})
	}
}
