package cli

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/auth"
	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/observe"
	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/ykt"
)

type localObserver struct {
	recorder *observe.Recorder
	session  *auth.Store
	log      *slog.Logger
	warn     sync.Once
}

func openObservation(dataDir string, session *auth.Store, logger *slog.Logger) (*localObserver, error) {
	from := os.Getenv("YKT_OBSERVE_FROM")
	if from == "" {
		return nil, nil
	}
	start, err := time.Parse(time.RFC3339, from)
	if err != nil {
		return nil, fmt.Errorf("YKT_OBSERVE_FROM 需要 RFC3339 时间")
	}
	if time.Now().Before(start) {
		return nil, nil
	}
	dir := os.Getenv("YKT_OBSERVE_DIR")
	if dir == "" {
		dir = filepath.Join(dataDir, "observations")
	}
	recorder, err := observe.New(dir, 24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("无法打开本地观察记录")
	}
	return &localObserver{recorder: recorder, session: session, log: logger}, nil
}

func (o *localObserver) Request(event ykt.RequestObservation) {
	o.check(o.recorder.RecordRequest(observe.Request{
		At: event.At, Endpoint: event.Endpoint, Status: event.Status,
		DurationMS: event.DurationMS, Outcome: event.Outcome,
		SessionCookie: event.SessionCookie, CSRFCookie: event.CSRFCookie,
	}))
}

func (o *localObserver) AuthResult(at time.Time, outcome string) {
	o.check(o.recorder.RecordAuth(at, o.session.Current().UpdatedAt, outcome))
}

func (o *localObserver) check(err error) {
	if err != nil {
		o.warn.Do(func() { o.log.Warn("观察记录写入失败，提醒服务继续运行") })
	}
}
