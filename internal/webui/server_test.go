package webui

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/domain"
	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/store"
)

func TestTasksScoresRemainPerClassroom(t *testing.T) {
	repo, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	zero, decimal, total := 0.0, 2.75, 5.0
	start := time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)
	tasks := []domain.Task{
		{ID: "ykt:1:101", Title: "练习一", Course: "示例课程", Type: domain.TaskHomework, Status: domain.StatusDone, Score: &zero, TotalScore: &total, URL: "https://example.org/1", StartAt: start},
		{ID: "ykt:2:101", Title: "练习一", Course: "示例课程", Type: domain.TaskHomework, Status: domain.StatusPending, Score: &decimal, TotalScore: &total, URL: "https://example.org/2"},
		{ID: "ykt:3:101", Title: "练习一", Course: "示例课程", Type: domain.TaskHomework, Status: domain.StatusPending},
	}
	if err := repo.SaveTasks(context.Background(), tasks); err != nil {
		t.Fatal(err)
	}
	s := &Server{repo: repo}
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/api/tasks", nil))
	if response.Code != 200 {
		t.Fatalf("status=%d", response.Code)
	}
	var payload struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Tasks) != 3 {
		t.Fatalf("API must retain three classroom records: %d", len(payload.Tasks))
	}
	byID := make(map[string]map[string]any)
	for _, item := range payload.Tasks {
		byID[item["id"].(string)] = item
	}
	for i, want := range []any{zero, decimal, nil} {
		item := byID[tasks[i].ID]
		if got, ok := item["score"]; !ok || got != want {
			t.Errorf("%s score=%v (present=%v), want %v", tasks[i].ID, got, ok, want)
		}
		if item["status"] != string(tasks[i].Status) || item["url"] != tasks[i].URL {
			t.Errorf("classroom status or URL changed: %v", item)
		}
	}
	if byID[tasks[0].ID]["total_score"] != total || byID[tasks[1].ID]["total_score"] != total || byID[tasks[2].ID]["total_score"] != nil {
		t.Fatal("total_score must preserve values and null")
	}
	gotStart, err := time.Parse(time.RFC3339, byID[tasks[0].ID]["start_at_iso"].(string))
	if err != nil || !gotStart.Equal(start) {
		t.Fatalf("start time changed: %v, %v", gotStart, err)
	}
}

func TestTaskViewModuleIsServed(t *testing.T) {
	response := httptest.NewRecorder()
	(&Server{}).Handler().ServeHTTP(response, httptest.NewRequest("GET", "/assets/task-view.mjs", nil))
	if response.Code != 200 || response.Header().Get("Content-Type") != "application/javascript; charset=utf-8" || response.Body.Len() == 0 {
		t.Fatalf("module asset unavailable: %d", response.Code)
	}
}
