// Package observe records passive, privacy-preserving request telemetry.
package observe

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	captureWindowFile       = "capture-window.json"
	maxLogBytes       int64 = 32 << 20
)

var (
	ErrClosed = errors.New("observe: recorder closed")

	errInvalidWindow = errors.New("observe: invalid capture window")
	errStorage       = errors.New("observe: storage failure")
	errRecordLimit   = errors.New("observe: auth log limit reached")
)

type Request struct {
	At            time.Time `json:"at"`
	Endpoint      string    `json:"endpoint"`
	Status        int       `json:"status"`
	DurationMS    int64     `json:"duration_ms"`
	Outcome       string    `json:"outcome"`
	SessionCookie bool      `json:"set_sessionid"`
	CSRFCookie    bool      `json:"set_csrftoken"`
}

type captureWindow struct {
	StartedAt string `json:"started_at"`
}

type authRecord struct {
	At         time.Time `json:"at"`
	AcquiredAt time.Time `json:"acquired_at"`
	Outcome    string    `json:"outcome"`
}

type Recorder struct {
	mu        sync.Mutex
	dir       string
	startedAt time.Time
	window    time.Duration
	closed    bool
}

// Persist the capture start so restarts do not create a new observation window.
func New(dir string, window time.Duration) (*Recorder, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, errStorage
	}

	startedAt, err := loadOrCreateWindow(dir)
	if err != nil {
		return nil, err
	}

	return &Recorder{
		dir:       dir,
		startedAt: startedAt,
		window:    window,
	}, nil
}

func loadOrCreateWindow(dir string) (time.Time, error) {
	path := filepath.Join(dir, captureWindowFile)
	b, err := os.ReadFile(path)
	if err == nil {
		var saved captureWindow
		if err := json.Unmarshal(b, &saved); err != nil || saved.StartedAt == "" {
			return time.Time{}, errInvalidWindow
		}
		startedAt, err := time.Parse(time.RFC3339, saved.StartedAt)
		if err != nil {
			return time.Time{}, errInvalidWindow
		}
		return startedAt.UTC(), nil
	}
	if !os.IsNotExist(err) {
		return time.Time{}, errStorage
	}

	startedAt := time.Now().UTC().Truncate(time.Second)
	b, err = json.Marshal(captureWindow{StartedAt: startedAt.Format(time.RFC3339)})
	if err != nil {
		return time.Time{}, errStorage
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return time.Time{}, errStorage
	}
	return startedAt, nil
}

func (r *Recorder) RecordRequest(req Request) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return ErrClosed
	}

	req.At = req.At.UTC()
	if !req.At.Before(r.startedAt.Add(r.window)) {
		return nil
	}
	req.Endpoint = normalizeEndpoint(req.Endpoint)
	req.Outcome = normalizeOutcome(req.Outcome)
	if req.DurationMS < 0 {
		req.DurationMS = 0
	}

	line, err := marshalLine(req)
	if err != nil {
		return errStorage
	}
	path := filepath.Join(r.dir, "requests-"+req.At.Format("2006-01-02")+".jsonl")
	_, err = appendLine(path, line)
	return err
}

func (r *Recorder) RecordAuth(at, acquiredAt time.Time, outcome string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return ErrClosed
	}

	record := authRecord{
		At:         at.UTC(),
		AcquiredAt: acquiredAt.UTC(),
		Outcome:    normalizeOutcome(outcome),
	}
	line, err := marshalLine(record)
	if err != nil {
		return errStorage
	}
	path := filepath.Join(r.dir, "auth-"+record.At.Format("2006-01")+".jsonl")
	dropped, err := appendLine(path, line)
	if err != nil {
		return err
	}
	if dropped {
		return errRecordLimit
	}
	return nil
}

func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

func appendLine(path string, line []byte) (bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return false, errStorage
	}

	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return false, errStorage
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return false, errStorage
	}
	if info.Size()+int64(len(line)) > maxLogBytes {
		_ = f.Close()
		return true, nil
	}
	if _, err := f.Write(line); err != nil {
		_ = f.Close()
		return false, errStorage
	}
	if err := f.Close(); err != nil {
		return false, errStorage
	}
	return false, nil
}

func marshalLine(v any) ([]byte, error) {
	line, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return append(line, '\n'), nil
}

func normalizeEndpoint(endpoint string) string {
	switch endpoint {
	case "basic_info", "courses", "chapters", "leaf_info", "exam_cover", "learn_logs", "course_meta", "other":
		return endpoint
	default:
		return "other"
	}
}

func normalizeOutcome(outcome string) string {
	switch outcome {
	case "ok", "auth_expired", "rate_limited", "not_found", "upstream", "canceled":
		return outcome
	default:
		return "upstream"
	}
}
