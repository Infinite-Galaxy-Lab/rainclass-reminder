package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/domain"
	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/ykt"
)

type memoryVault struct {
	value string
	fail  bool
}

func (v *memoryVault) Load() (string, error) { return v.value, nil }
func (v *memoryVault) Save(s string) error {
	if v.fail {
		return errors.New("synthetic vault failure")
	}
	v.value = s
	return nil
}

type testSink struct {
	calls int
	fail  bool
}

func (s *testSink) Notify(_, _ string) error {
	s.calls++
	if s.fail {
		return errors.New("synthetic notification failure")
	}
	return nil
}

type fixture struct {
	account      atomic.Int64
	status       atomic.Int64
	deadline     atomic.Int64
	courseStatus atomic.Int64
	block        atomic.Bool
	entered      chan struct{}
	options      ykt.Options
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{entered: make(chan struct{}, 1)}
	f.account.Store(101)
	f.deadline.Store(time.Now().Add(5 * time.Minute).UnixMilli())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(405)
			return
		}
		if code := f.status.Load(); code != 0 {
			w.WriteHeader(int(code))
			return
		}
		if f.block.Load() {
			select {
			case f.entered <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		deadline := f.deadline.Load()
		switch {
		case r.URL.Path == "/v/course_meta/user_info":
			fmt.Fprint(w, `{"success":true,"data":{}}`)
		case r.URL.Path == "/api/v3/user/basic-info":
			fmt.Fprintf(w, `{"success":true,"data":{"user_id":%d}}`, f.account.Load())
		case r.URL.Path == "/v2/api/web/courses/list":
			if code := f.courseStatus.Load(); code != 0 {
				w.WriteHeader(int(code))
				return
			}
			fmt.Fprint(w, `{"success":true,"data":{"list":[{"classroom_id":11,"name":"合成课程"}]}}`)
		case strings.Contains(r.URL.Path, "/course/chapter"):
			fmt.Fprintf(w, `{"success":true,"data":{"course_chapter":[{"section_leaf_list":[{"id":21,"leaf_type":5,"name":"合成作业","score_deadline":%d}]}]}}`, deadline)
		case strings.Contains(r.URL.Path, "/leaf_info/"):
			fmt.Fprintf(w, `{"success":true,"data":{"name":"合成作业","score_deadline":%d,"content_info":{"score_evaluation":{"name":"作业"}}}}`, deadline)
		case strings.Contains(r.URL.Path, "/logs/learn/"):
			fmt.Fprint(w, `{"success":true,"data":{"activities":[{"title":"合成作业","type":4,"courseware_id":31,"status":1,"score":0,"total_score":12.5}]}}`)
		default:
			t.Errorf("unexpected GET: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	f.options = ykt.Options{BaseURL: server.URL, MinRequestGap: time.Nanosecond}
	return f
}

func TestMobileUsesIndependentDeviceTimeZones(t *testing.T) {
	f := newFixture(t)
	now := time.Date(2026, 11, 10, 16, 30, 0, 0, time.UTC)
	f.deadline.Store(now.Add(2 * time.Hour).UnixMilli())
	global := time.Local
	sink := &testSink{}
	m := openTestMobile(t, t.TempDir(), &memoryVault{}, sink, f)
	m.clock = func() time.Time { return now }
	if err := m.SetTimeZone("Asia/Shanghai"); err != nil {
		t.Fatal(err)
	}
	if err := m.Authenticate(syntheticCookie); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Refresh(); err != nil {
		t.Fatal(err)
	}
	if sink.calls != 0 {
		t.Fatal("device quiet hours ignored")
	}
	if err := m.SetTimeZone("Invalid/Zone"); err == nil {
		t.Fatal("invalid zone accepted")
	}
	if m.location.String() != "Asia/Shanghai" {
		t.Fatal("invalid zone replaced device zone")
	}
	otherSink := &testSink{}
	other := openTestMobile(t, t.TempDir(), &memoryVault{}, otherSink, f)
	other.clock = m.clock
	if err := other.SetTimeZone("UTC"); err != nil {
		t.Fatal(err)
	}
	if err := other.Authenticate(syntheticCookie); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Refresh(); err != nil {
		t.Fatal(err)
	}
	if otherSink.calls != 1 {
		t.Fatal("engine zones are not independent")
	}
	if _, err := m.Refresh(); err != nil {
		t.Fatal(err)
	}
	if sink.calls != 0 || time.Local != global {
		t.Fatal("another engine changed local policy")
	}
	if err := m.SetTimeZone("UTC"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Refresh(); err != nil {
		t.Fatal(err)
	}
	if sink.calls != 1 {
		t.Fatal("device zone change did not release deferred reminder")
	}
}

func TestMobileRejectsUnknownDeviceZoneBeforeOpeningStorage(t *testing.T) {
	for _, zone := range []string{"", "Local", "Invalid/Zone"} {
		dir := filepath.Join(t.TempDir(), "must-not-exist")
		if _, err := OpenMobile(dir, &memoryVault{}, &testSink{}, zone); err == nil || err.Error() != "timezone" {
			t.Fatalf("zone %q: %v", zone, err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("invalid zone created storage")
		}
	}
}

func TestMobileCanceledRefreshDoesNotStartOrCancelLaterWork(t *testing.T) {
	f := newFixture(t)
	m := openTestMobile(t, t.TempDir(), &memoryVault{}, &testSink{}, f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.RefreshContext(ctx); err == nil || err.Error() != "canceled" {
		t.Fatalf("pre-start cancellation: %v", err)
	}
	if err := m.Authenticate(syntheticCookie); err != nil {
		t.Fatal(err)
	}
	f.block.Store(true)
	done := make(chan error, 1)
	active, stop := context.WithCancel(context.Background())
	go func() { _, err := m.RefreshContext(active); done <- err }()
	select {
	case <-f.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	stop()
	select {
	case err := <-done:
		if err == nil || err.Error() != "canceled" {
			t.Fatalf("active cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request not interrupted")
	}
	f.block.Store(false)
	if _, err := m.Refresh(); err != nil {
		t.Fatalf("later refresh canceled: %v", err)
	}
}

func TestMobileAuthAlertRetriesDeduplicatesAndRecovers(t *testing.T) {
	f, sink := newFixture(t), &testSink{fail: true}
	dir, vault := t.TempDir(), &memoryVault{}
	m := openTestMobile(t, dir, vault, sink, f)
	if err := m.Authenticate(syntheticCookie); err != nil {
		t.Fatal(err)
	}
	f.courseStatus.Store(401)
	refreshExpired := func() {
		t.Helper()
		if _, err := m.Refresh(); err == nil || err.Error() != "auth_expired" {
			t.Fatalf("expiry: %v", err)
		}
	}
	refreshExpired()
	if sink.calls != 1 {
		t.Fatal("missing rejected notification attempt")
	}
	sink.fail = false
	refreshExpired()
	if sink.calls != 2 {
		t.Fatal("failed alert not retried")
	}
	refreshExpired()
	if sink.calls != 2 {
		t.Fatal("same expiry alerted twice")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m = openTestMobile(t, dir, vault, sink, f)
	refreshExpired()
	if sink.calls != 2 {
		t.Fatal("expiry deduplication lost on restart")
	}
	f.courseStatus.Store(0)
	f.deadline.Store(time.Now().Add(8 * 24 * time.Hour).UnixMilli())
	if _, err := m.Refresh(); err != nil {
		t.Fatal(err)
	}
	if readSnapshot(t, m).AuthState != "ready" {
		t.Fatal("login did not recover")
	}
	f.status.Store(503)
	if _, err := m.Refresh(); err == nil || err.Error() != "network" {
		t.Fatalf("network: %v", err)
	}
	if sink.calls != 2 || readSnapshot(t, m).AuthState != "ready" {
		t.Fatal("network failure classified as expiry")
	}
	f.status.Store(401)
	refreshExpired()
	if sink.calls != 3 {
		t.Fatal("new expiry did not alert after recovery")
	}
}

func openTestMobile(t *testing.T, dir string, v *memoryVault, sink *testSink, f *fixture) *Mobile {
	t.Helper()
	m, err := openMobile(dir, v, sink, f.options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func readSnapshot(t *testing.T, m *Mobile) Snapshot {
	t.Helper()
	raw, err := m.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var result Snapshot
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

const syntheticCookie = "sessionid=synthetic-session; csrftoken=synthetic-csrf"

func TestMobileRefreshReopenAndSignOut(t *testing.T) {
	f, dir, vault, sink := newFixture(t), t.TempDir(), &memoryVault{}, &testSink{}
	m := openTestMobile(t, dir, vault, sink, f)
	if _, err := m.Refresh(); err == nil || err.Error() != "session_required" {
		t.Fatalf("unsigned refresh: %v", err)
	}
	if err := m.Authenticate(syntheticCookie); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Refresh(); err != nil {
		t.Fatal(err)
	}
	view := readSnapshot(t, m)
	if view.AuthState != "ready" || view.SchemaVersion != 1 || view.FetchedAt == 0 || len(view.Tasks) != 1 {
		t.Fatalf("snapshot: %+v", view)
	}
	task := view.Tasks[0]
	if task.Score == nil || *task.Score != 0 || task.TotalScore == nil || *task.TotalScore != 12.5 || task.Status != "pending" {
		t.Fatalf("task: %+v", task)
	}
	if !strings.Contains(task.URL, "/quizSummary/") {
		t.Fatalf("independent route lost: %s", task.URL)
	}
	first := sink.calls
	if first != 1 {
		t.Fatalf("notifications = %d, want 1", first)
	}
	if _, err := m.Refresh(); err != nil {
		t.Fatal(err)
	}
	if sink.calls != first {
		t.Fatal("duplicate notification")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m = openTestMobile(t, dir, vault, sink, f)
	if got := readSnapshot(t, m); len(got.Tasks) != 1 || got.Tasks[0].Score == nil {
		t.Fatalf("reopen: %+v", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "synthetic-session") {
			t.Fatal("plaintext session in core files")
		}
	}
	if err := m.SignOut(); err != nil {
		t.Fatal(err)
	}
	if vault.value != "" {
		t.Fatal("vault not cleared")
	}
	if view := readSnapshot(t, m); view.AuthState != "signed_out" || len(view.Tasks) != 0 {
		t.Fatalf("logout: %+v", view)
	}
	keys, err := m.repo.PushedKeys(context.Background())
	if err != nil || len(keys) != 0 {
		t.Fatalf("push records remain: %v %v", keys, err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m = openTestMobile(t, dir, vault, sink, f)
	if got := readSnapshot(t, m); got.AuthState != "signed_out" || len(got.Tasks) != 0 {
		t.Fatal("logout did not persist")
	}
}

func TestMobileAccountSwitchAndInterruptedCommit(t *testing.T) {
	f, dir, vault, sink := newFixture(t), t.TempDir(), &memoryVault{}, &testSink{}
	m := openTestMobile(t, dir, vault, sink, f)
	if err := m.Authenticate(syntheticCookie); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Refresh(); err != nil {
		t.Fatal(err)
	}
	f.status.Store(401)
	if err := m.Authenticate(syntheticCookie); err == nil {
		t.Fatal("invalid account accepted")
	}
	if len(readSnapshot(t, m).Tasks) != 1 {
		t.Fatal("failed authentication erased cache")
	}
	f.status.Store(0)
	f.account.Store(202)
	vault.fail = true
	if err := m.Authenticate(syntheticCookie); err == nil || err.Error() != "vault" {
		t.Fatalf("vault failure: %v", err)
	}
	if len(readSnapshot(t, m).Tasks) != 1 {
		t.Fatal("failed vault write erased cache")
	}
	vault.fail = false
	if err := m.Authenticate(syntheticCookie); err != nil {
		t.Fatal(err)
	}
	if len(readSnapshot(t, m).Tasks) != 0 {
		t.Fatal("previous account cache exposed")
	}
	if _, err := m.Refresh(); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	var saved credentials
	if err := json.Unmarshal([]byte(vault.value), &saved); err != nil {
		t.Fatal(err)
	}
	saved.Account = "303"
	raw, _ := json.Marshal(saved)
	vault.value = string(raw)
	m = openTestMobile(t, dir, vault, sink, f)
	if got := readSnapshot(t, m); len(got.Tasks) != 0 {
		t.Fatal("interrupted account switch exposed previous cache")
	}
}

func TestMobileNotificationFailureRetries(t *testing.T) {
	f, vault, sink := newFixture(t), &memoryVault{}, &testSink{fail: true}
	m := openTestMobile(t, t.TempDir(), vault, sink, f)
	if err := m.Authenticate(syntheticCookie); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Refresh(); err != nil {
		t.Fatal(err)
	}
	if sink.calls != 1 {
		t.Fatalf("first notification attempts: %d", sink.calls)
	}
	sink.fail = false
	if _, err := m.Refresh(); err != nil {
		t.Fatal(err)
	}
	if sink.calls != 2 {
		t.Fatal("failed notification was marked sent")
	}
	if _, err := m.Refresh(); err != nil {
		t.Fatal(err)
	}
	if sink.calls != 2 {
		t.Fatal("successful retry sent again")
	}
}

func TestMobileAuthExpiryAndCancellation(t *testing.T) {
	f, sink := newFixture(t), &testSink{}
	m := openTestMobile(t, t.TempDir(), &memoryVault{}, sink, f)
	if err := m.Authenticate(syntheticCookie); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Refresh(); err != nil {
		t.Fatal(err)
	}
	for _, code := range []int64{401, 429, 503} {
		f.status.Store(code)
		_, err := m.Refresh()
		want := map[int64]string{401: "auth_expired", 429: "rate_limited", 503: "network"}[code]
		if err == nil || err.Error() != want {
			t.Fatalf("status %d: %v, want %s", code, err, want)
		}
		if len(readSnapshot(t, m).Tasks) != 1 {
			t.Fatal("failed refresh erased cache")
		}
		if code == 401 && readSnapshot(t, m).AuthState != "expired" {
			t.Fatal("expired state not exposed")
		}
	}
	f.status.Store(0)
	f.block.Store(true)
	done := make(chan error, 1)
	go func() { _, err := m.Refresh(); done <- err }()
	select {
	case <-f.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	m.Cancel()
	select {
	case err := <-done:
		if err == nil || err.Error() != "canceled" {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not interrupt request")
	}
}

func TestMobileSnapshotPreservesMissingScore(t *testing.T) {
	f := newFixture(t)
	m := openTestMobile(t, t.TempDir(), &memoryVault{}, &testSink{}, f)
	if err := m.Authenticate(syntheticCookie); err != nil {
		t.Fatal(err)
	}
	if err := m.repo.SaveTasks(context.Background(), []domain.Task{{ID: "synthetic", Title: "未提供成绩", Type: domain.TaskVideo}}); err != nil {
		t.Fatal(err)
	}
	got := readSnapshot(t, m)
	if len(got.Tasks) != 1 || got.Tasks[0].Score != nil || got.Tasks[0].TotalScore != nil || got.Tasks[0].Deadline != 0 {
		t.Fatalf("nullable snapshot: %+v", got)
	}
}

func TestMobileSessionRotationPersistsOnlyCredentials(t *testing.T) {
	v := &memoryVault{}
	f := newFixture(t)
	m := openTestMobile(t, t.TempDir(), v, &testSink{}, f)
	if err := m.Authenticate(syntheticCookie); err != nil {
		t.Fatal(err)
	}
	before := v.value
	if err := m.session.ObserveCookie("unrelated", "synthetic-private-value"); err != nil {
		t.Fatal(err)
	}
	if v.value != before {
		t.Fatal("unrelated cookie persisted")
	}
	if err := m.session.ObserveCookie("sessionid", "synthetic-rotated"); err != nil {
		t.Fatal(err)
	}
	var saved credentials
	if err := json.Unmarshal([]byte(v.value), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Cookie.SessionID != "synthetic-rotated" || saved.Account != "101" {
		t.Fatal("rotated credentials not persisted")
	}
	if err := m.session.ObserveCookie("sessionid", ""); err != nil {
		t.Fatal(err)
	}
	if !m.session.Valid() {
		t.Fatal("empty response cookie destroyed session")
	}
}
