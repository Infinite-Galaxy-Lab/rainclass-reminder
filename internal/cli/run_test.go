package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/auth"
	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/config"
)

func TestRunKeepsPanelAndLoopAliveWhenStartupCheckFails(t *testing.T) {
	for _, status := range []int{401, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			cycle := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v2/api/web/courses/list" {
					select {
					case cycle <- struct{}{}:
					default:
					}
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			cfg := config.Default()
			cfg.DataDir = t.TempDir()
			cfg.Ykt.BaseURL = server.URL
			cfg.Notify.Enabled = nil
			cfg.WebUI.Enabled = true
			cfg.WebUI.Listen = "127.0.0.1:0"
			cfg.Poll.MinRequestGap = time.Nanosecond
			store, err := auth.NewStore(filepath.Join(cfg.DataDir, "cookie.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Save(auth.Cookie{SessionID: "synthetic-only"}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var out, errors bytes.Buffer
			done := make(chan int, 1)
			go func() { done <- cmdRun(ctx, cfg, nil, &out, &errors) }()
			select {
			case <-cycle:
			case code := <-done:
				t.Fatalf("startup failure exited before background cycle: %d, %s", code, errors.String())
			case <-time.After(5 * time.Second):
				t.Fatal("background cycle did not start")
			}
			cancel()
			select {
			case code := <-done:
				if code != 0 || !strings.Contains(out.String(), "本地面板") {
					t.Fatalf("panel not retained: %d %s", code, out.String())
				}
			case <-time.After(3 * time.Second):
				t.Fatal("shutdown did not finish")
			}
		})
	}
}
