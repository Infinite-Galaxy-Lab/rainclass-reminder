package ykt

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type observationSink struct {
	requests []RequestObservation
	auth     []string
}

func (s *observationSink) Request(event RequestObservation)       { s.requests = append(s.requests, event) }
func (s *observationSink) AuthResult(_ time.Time, outcome string) { s.auth = append(s.auth, outcome) }

func TestObservationPreservesAuthClassificationAndRequestCount(t *testing.T) {
	cases := []struct {
		name, body, requestOutcome, authOutcome string
		status                                  int
	}{
		{"success", `{"success":true,"data":{}}`, "ok", "ok", 200},
		{"http auth", `{}`, "auth_expired", "auth_expired", 401},
		{"envelope auth", `{"success":false,"errcode":401,"errmsg":"not login"}`, "ok", "auth_expired", 200},
		{"html auth", `<html>login</html>`, "auth_expired", "auth_expired", 200},
		{"offline upstream", `{}`, "upstream", "upstream", 503},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet {
					t.Errorf("unexpected method: %s", r.Method)
				}
				w.Header().Add("Set-Cookie", "sessionid=synthetic-session-value; Path=/")
				w.Header().Add("Set-Cookie", "csrftoken=synthetic-csrf-value; Path=/")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			sink := &observationSink{}
			client, err := New(Options{BaseURL: srv.URL, Observer: sink})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.BasicInfo(context.Background())
			if errors.Is(err, ErrAuthExpired) != (tc.authOutcome == "auth_expired") {
				t.Fatalf("classification changed: %v", err)
			}
			if calls.Load() != 1 || len(sink.requests) != 1 || len(sink.auth) != 1 {
				t.Fatalf("observer must not add requests: %d, %+v", calls.Load(), sink)
			}
			event := sink.requests[0]
			if event.Endpoint != "basic_info" || event.Status != tc.status || event.Outcome != tc.requestOutcome || sink.auth[0] != tc.authOutcome || !event.SessionCookie || !event.CSRFCookie {
				t.Fatalf("unexpected observation: %+v, auth=%v", event, sink.auth)
			}
			data, _ := json.Marshal(event)
			if strings.Contains(string(data), "synthetic-") || strings.Contains(string(data), srv.URL) {
				t.Fatal("observation leaked response values")
			}
		})
	}
}

func TestObservationRedactsUnknownPathsAndTracksCourses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"list":[]}}`))
	}))
	defer srv.Close()
	sink := &observationSink{}
	client, err := New(Options{BaseURL: srv.URL, Observer: sink, MinRequestGap: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := client.getJSON(context.Background(), "/private-name/12345", nil, &out); err != nil {
		t.Fatal(err)
	}
	if sink.requests[0].Endpoint != "other" {
		t.Fatalf("unknown endpoint not redacted: %s", sink.requests[0].Endpoint)
	}
	if _, err := client.Courses(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.auth) != 1 || sink.auth[0] != "ok" || sink.requests[1].Endpoint != "courses" {
		t.Fatalf("courses must provide passive auth evidence: %+v", sink)
	}
}
