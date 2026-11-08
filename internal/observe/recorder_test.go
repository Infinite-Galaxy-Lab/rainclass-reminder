package observe

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCaptureWindowPersistsAcrossRestartAndOnlyAuthContinuesAfterWindow(t *testing.T) {
	dir := t.TempDir()
	window := 24 * time.Hour

	first, err := New(dir, window)
	if err != nil {
		t.Fatal(err)
	}
	startedAt := first.startedAt
	windowPath := filepath.Join(dir, captureWindowFile)
	before, err := os.ReadFile(windowPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := New(dir, window)
	if err != nil {
		t.Fatal(err)
	}
	if !second.startedAt.Equal(startedAt) {
		t.Fatalf("started_at changed across restart: got %v, want %v", second.startedAt, startedAt)
	}
	after, err := os.ReadFile(windowPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("capture-window.json was rewritten on restart")
	}

	inside := startedAt.Add(window - time.Minute)
	boundary := startedAt.Add(window)
	outside := startedAt.Add(window + time.Minute)
	if err := second.RecordRequest(Request{
		At:         inside,
		Endpoint:   "basic_info",
		Status:     200,
		DurationMS: 4,
		Outcome:    "ok",
	}); err != nil {
		t.Fatal(err)
	}
	if err := second.RecordRequest(Request{At: boundary, Endpoint: "courses", Outcome: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := second.RecordRequest(Request{At: outside, Endpoint: "courses", Outcome: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := second.RecordAuth(outside, outside.Add(-time.Hour), "ok"); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	requests := readJSONObjects(t, requestLogPath(dir, inside))
	if len(requests) != 1 {
		t.Fatalf("got %d request records, want only the in-window record", len(requests))
	}
	auths := readJSONObjects(t, authLogPath(dir, outside))
	if len(auths) != 1 {
		t.Fatalf("got %d auth records, want 1", len(auths))
	}
}

func TestRecordsUseOnlyWhitelistedFieldsAndSanitizeInput(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	const sentinel = "LEAK_SENTINEL_URL_TOKEN"
	at := time.Date(2026, time.November, 8, 10, 11, 12, 0, time.FixedZone("test", 8*60*60))
	if err := r.RecordRequest(Request{
		At:            at,
		Endpoint:      "https://secret.invalid/path?token=" + sentinel,
		Status:        418,
		DurationMS:    -17,
		Outcome:       sentinel,
		SessionCookie: true,
		CSRFCookie:    true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordAuth(at, at.Add(-time.Hour), sentinel); err != nil {
		t.Fatal(err)
	}

	requestPath := requestLogPath(dir, at)
	requestBytes, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(requestBytes, []byte(sentinel)) {
		t.Fatal("request output contains untrusted input")
	}
	requests := readJSONObjects(t, requestPath)
	assertKeys(t, requests[0], []string{
		"at", "duration_ms", "endpoint", "outcome", "set_csrftoken", "set_sessionid", "status",
	})
	if got := decodeString(t, requests[0], "endpoint"); got != "other" {
		t.Fatalf("endpoint = %q, want other", got)
	}
	if got := decodeString(t, requests[0], "outcome"); got != "upstream" {
		t.Fatalf("outcome = %q, want upstream", got)
	}
	if got := decodeInt64(t, requests[0], "duration_ms"); got != 0 {
		t.Fatalf("duration_ms = %d, want 0", got)
	}
	if got := decodeBool(t, requests[0], "set_sessionid"); !got {
		t.Fatal("set_sessionid = false, want true")
	}
	if got := decodeBool(t, requests[0], "set_csrftoken"); !got {
		t.Fatal("set_csrftoken = false, want true")
	}
	if got := decodeString(t, requests[0], "at"); !strings.HasSuffix(got, "Z") {
		t.Fatalf("at = %q, want UTC RFC3339", got)
	}

	authPath := authLogPath(dir, at)
	authBytes, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(authBytes, []byte(sentinel)) {
		t.Fatal("auth output contains untrusted input")
	}
	auths := readJSONObjects(t, authPath)
	assertKeys(t, auths[0], []string{"acquired_at", "at", "outcome"})
	if got := decodeString(t, auths[0], "outcome"); got != "upstream" {
		t.Fatalf("auth outcome = %q, want upstream", got)
	}
	if got := decodeString(t, auths[0], "at"); !strings.HasSuffix(got, "Z") {
		t.Fatalf("auth at = %q, want UTC RFC3339", got)
	}
	if got := decodeString(t, auths[0], "acquired_at"); !strings.HasSuffix(got, "Z") {
		t.Fatalf("acquired_at = %q, want UTC RFC3339", got)
	}
}

func TestConcurrentWritesProduceValidJSONLines(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	const writers = 12
	const recordsPerWriter = 25
	base := r.startedAt.Add(time.Minute)
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < recordsPerWriter; j++ {
				if err := r.RecordRequest(Request{
					At:         base.Add(time.Duration(worker*recordsPerWriter+j) * time.Millisecond),
					Endpoint:   "courses",
					Status:     200,
					DurationMS: int64(j),
					Outcome:    "ok",
				}); err != nil {
					errs <- err
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	records := readJSONObjects(t, requestLogPath(dir, base))
	if len(records) != writers*recordsPerWriter {
		t.Fatalf("got %d records, want %d", len(records), writers*recordsPerWriter)
	}
}

func TestCloseRejectsSubsequentWrites(t *testing.T) {
	r, err := New(t.TempDir(), 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close returned %v", err)
	}

	now := time.Now()
	if err := r.RecordRequest(Request{At: now}); !errors.Is(err, ErrClosed) {
		t.Fatalf("RecordRequest error = %v, want ErrClosed", err)
	}
	if err := r.RecordAuth(now, now, "ok"); !errors.Is(err, ErrClosed) {
		t.Fatalf("RecordAuth error = %v, want ErrClosed", err)
	}
}

func TestInvalidCaptureWindowIsRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, captureWindowFile), []byte(`{"started_at":"not-rfc3339"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir, 24*time.Hour); !errors.Is(err, errInvalidWindow) {
		t.Fatalf("New error = %v, want errInvalidWindow", err)
	}
}

func TestFileLimitsDropRequestsAndRejectAuth(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	at := r.startedAt.Add(time.Minute)
	requestPath := requestLogPath(dir, at)
	truncateFile(t, requestPath, maxLogBytes)
	if err := r.RecordRequest(Request{At: at, Endpoint: "courses", Outcome: "ok"}); err != nil {
		t.Fatalf("full request log returned error: %v", err)
	}
	info, err := os.Stat(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != maxLogBytes {
		t.Fatalf("request log size = %d, want %d", info.Size(), maxLogBytes)
	}

	authPath := authLogPath(dir, at)
	truncateFile(t, authPath, maxLogBytes)
	if err := r.RecordAuth(at, at, "ok"); !errors.Is(err, errRecordLimit) {
		t.Fatalf("full auth log error = %v, want errRecordLimit", err)
	}
}

func requestLogPath(dir string, at time.Time) string {
	return filepath.Join(dir, "requests-"+at.UTC().Format("2006-01-02")+".jsonl")
}

func authLogPath(dir string, at time.Time) string {
	return filepath.Join(dir, "auth-"+at.UTC().Format("2006-01")+".jsonl")
}

func truncateFile(t *testing.T, path string, size int64) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func readJSONObjects(t *testing.T, path string) []map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(b), []byte{'\n'})
	if len(lines) == 1 && len(lines[0]) == 0 {
		return nil
	}
	records := make([]map[string]json.RawMessage, 0, len(lines))
	for _, line := range lines {
		var record map[string]json.RawMessage
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("invalid JSON line: %v", err)
		}
		records = append(records, record)
	}
	return records
}

func assertKeys(t *testing.T, record map[string]json.RawMessage, want []string) {
	t.Helper()
	got := make([]string, 0, len(record))
	for key := range record {
		got = append(got, key)
	}
	if len(got) != len(want) {
		t.Fatalf("field keys = %v, want exactly %v", got, want)
	}
	for _, key := range want {
		if _, ok := record[key]; !ok {
			t.Fatalf("field keys = %v, missing %q", got, key)
		}
	}
}

func decodeString(t *testing.T, record map[string]json.RawMessage, key string) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(record[key], &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func decodeInt64(t *testing.T, record map[string]json.RawMessage, key string) int64 {
	t.Helper()
	var value int64
	if err := json.Unmarshal(record[key], &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func decodeBool(t *testing.T, record map[string]json.RawMessage, key string) bool {
	t.Helper()
	var value bool
	if err := json.Unmarshal(record[key], &value); err != nil {
		t.Fatal(err)
	}
	return value
}
