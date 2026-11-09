package store

import (
	"context"
	"testing"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/domain"
)

func seedReset(t *testing.T, repo *SQLite) {
	t.Helper()
	ctx := context.Background()
	if err := repo.SaveTasks(ctx, []domain.Task{{ID: "synthetic-task", Title: "合成任务"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Claim(ctx, "synthetic-push"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetKV(ctx, "synthetic-key", "synthetic-value"); err != nil {
		t.Fatal(err)
	}
}

func TestResetClearsAllDataAndKeepsSchema(t *testing.T) {
	repo := newTestRepo(t)
	seedReset(t, repo)
	if err := repo.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"tasks", "pushes", "kv"} {
		var count int
		if err := repo.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("%s has %d records after reset", table, count)
		}
	}
	seedReset(t, repo)
}

func TestResetRollsBackOnFailure(t *testing.T) {
	repo := newTestRepo(t)
	seedReset(t, repo)
	if _, err := repo.db.Exec(`CREATE TRIGGER prevent_reset BEFORE DELETE ON pushes BEGIN SELECT RAISE(ABORT, 'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := repo.Reset(context.Background()); err == nil {
		t.Fatal("reset ignored trigger failure")
	}
	for _, table := range []string{"tasks", "pushes"} {
		var count int
		if err := repo.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("%s lost data on failed reset", table)
		}
	}
	value, err := repo.GetKV(context.Background(), "synthetic-key")
	if err != nil || value != "synthetic-value" {
		t.Fatalf("kv lost after failed reset: %q %v", value, err)
	}
}
