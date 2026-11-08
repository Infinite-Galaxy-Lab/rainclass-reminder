package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/domain"
)

func newTestRepo(t *testing.T) *SQLite {
	t.Helper()
	repo, err := Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

func TestClaimIsAtomic(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	ok, err := repo.Claim(ctx, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("首次占用应成功")
	}

	again, err := repo.Claim(ctx, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if again {
		t.Error("重复占用应失败（防止重复推送）")
	}
}

func TestClaimedNotCountedAsSent(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	if _, err := repo.Claim(ctx, "k1"); err != nil {
		t.Fatal(err)
	}
	keys, err := repo.PushedKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if keys["k1"] {
		t.Error("claimed 状态不应算作已发送")
	}
}

func TestRecordMakesKeySent(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	if _, err := repo.Claim(ctx, "k1"); err != nil {
		t.Fatal(err)
	}
	err := repo.Record(ctx, PushedRecord{
		Key: "k1", TaskID: "t1", TaskTitle: "作业", Kind: "before_deadline",
		Offset: 15, FireAt: time.Now(), SentAt: time.Now(), Channel: "feishu", Status: "sent",
	})
	if err != nil {
		t.Fatal(err)
	}

	keys, _ := repo.PushedKeys(ctx)
	if !keys["k1"] {
		t.Error("sent 状态应计入已发送")
	}

	// 已发送的键不应被再次占用。
	if ok, _ := repo.Claim(ctx, "k1"); ok {
		t.Error("已发送的键不应允许再次占用")
	}
}

func TestFailedKeyCanBeRetried(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	if _, err := repo.Claim(ctx, "k1"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Record(ctx, PushedRecord{
		Key: "k1", Status: "failed", Error: "网络错误",
		FireAt: time.Now(), SentAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	keys, _ := repo.PushedKeys(ctx)
	if keys["k1"] {
		t.Error("failed 不应计入已发送（需要重试）")
	}

	ok, err := repo.Claim(ctx, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("failed 状态的键应允许重新占用")
	}
}

func TestSuppressedCountsAsHandled(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	if _, err := repo.Claim(ctx, "k1"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Record(ctx, PushedRecord{
		Key: "k1", Status: "suppressed", Error: "coalesced",
		FireAt: time.Now(), SentAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	keys, _ := repo.PushedKeys(ctx)
	if !keys["k1"] {
		t.Error("suppressed 应计入已处理，避免每轮重复出现")
	}
}

func TestRecentPushes(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	now := time.Now()

	for i, key := range []string{"a", "b", "c"} {
		if _, err := repo.Claim(ctx, key); err != nil {
			t.Fatal(err)
		}
		if err := repo.Record(ctx, PushedRecord{
			Key: key, TaskID: "t", TaskTitle: "任务" + key, Status: "sent",
			FireAt: now, SentAt: now.Add(time.Duration(i) * time.Minute), Channel: "feishu",
		}); err != nil {
			t.Fatal(err)
		}
	}

	recs, err := repo.RecentPushes(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("limit=2 应返回 2 条，实际 %d", len(recs))
	}
	// 应按时间倒序
	if !recs[0].SentAt.After(recs[1].SentAt) && !recs[0].SentAt.Equal(recs[1].SentAt) {
		t.Error("应按发送时间倒序")
	}
}

func TestTasksSnapshot(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	now := time.Now()

	tasks := []domain.Task{
		{ID: "a", Type: domain.TaskHomework, Title: "作业", Course: "高数", Deadline: now.Add(time.Hour), Status: domain.StatusPending},
		{ID: "b", Type: domain.TaskExam, Title: "考试", Course: "线代", Status: domain.StatusUnknown},
	}
	if err := repo.SaveTasks(ctx, tasks); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveSnapshotTime(ctx, now); err != nil {
		t.Fatal(err)
	}

	snap, err := repo.LoadTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Tasks) != 2 {
		t.Fatalf("应有 2 条任务，实际 %d", len(snap.Tasks))
	}
	// 有截止的应排在前面
	if snap.Tasks[0].ID != "a" {
		t.Errorf("有截止任务应排前，实际首条 %q", snap.Tasks[0].ID)
	}
	if snap.Tasks[0].Deadline.IsZero() {
		t.Error("截止时间应被持久化")
	}
	if snap.Tasks[1].Type != domain.TaskExam {
		t.Errorf("类型失真的: %v", snap.Tasks[1].Type)
	}
	if snap.FetchedAt.IsZero() {
		t.Error("快照时间应被持久化")
	}
}

func TestSaveTasksReplaces(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	if err := repo.SaveTasks(ctx, []domain.Task{{ID: "a", Title: "旧", Type: domain.TaskHomework}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveTasks(ctx, []domain.Task{{ID: "b", Title: "新", Type: domain.TaskExam}}); err != nil {
		t.Fatal(err)
	}

	snap, _ := repo.LoadTasks(ctx)
	if len(snap.Tasks) != 1 || snap.Tasks[0].ID != "b" {
		t.Errorf("应整体替换任务表，实际 %+v", snap.Tasks)
	}
}

func TestTaskScoresRoundTrip(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	zero, decimal := 0.0, 2.75
	decimalTotal := 4.5

	tasks := []domain.Task{
		{ID: "missing", Type: domain.TaskHomework, Title: "缺失"},
		{ID: "zero", Type: domain.TaskHomework, Title: "零分", Score: &zero},
		{ID: "decimal", Type: domain.TaskExam, Title: "小数", Score: &decimal, TotalScore: &decimalTotal},
	}
	if err := repo.SaveTasks(ctx, tasks); err != nil {
		t.Fatal(err)
	}

	snap, err := repo.LoadTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]domain.Task, len(snap.Tasks))
	for _, task := range snap.Tasks {
		got[task.ID] = task
	}

	if task := got["missing"]; task.Score != nil || task.TotalScore != nil {
		t.Errorf("NULL 应回读为 nil: score=%v total=%v", task.Score, task.TotalScore)
	}
	if task := got["zero"]; task.Score == nil || *task.Score != 0 || task.TotalScore != nil {
		t.Errorf("零分应保留且总分保持 nil: score=%v total=%v", task.Score, task.TotalScore)
	}
	if task := got["decimal"]; task.Score == nil || *task.Score != 2.75 ||
		task.TotalScore == nil || *task.TotalScore != 4.5 {
		t.Errorf("小数分数未往返: score=%v total=%v", task.Score, task.TotalScore)
	}

	if got["zero"].Score == got["decimal"].Score {
		t.Error("每行的可选分数应使用独立指针")
	}
}

func TestOpenMigratesTaskScoreColumns(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/legacy.db"
	ctx := context.Background()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	const legacySchema = `
CREATE TABLE pushes (
    key        TEXT PRIMARY KEY,
    task_id    TEXT NOT NULL,
    task_title TEXT NOT NULL,
    kind       TEXT NOT NULL,
    offset_min INTEGER NOT NULL DEFAULT 0,
    fire_at    INTEGER NOT NULL,
    sent_at    INTEGER NOT NULL,
    channel    TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL,
    error      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_pushes_sent_at ON pushes(sent_at DESC);
CREATE TABLE tasks (
    id        TEXT PRIMARY KEY,
    type      TEXT NOT NULL,
    title     TEXT NOT NULL,
    course    TEXT NOT NULL DEFAULT '',
    deadline  INTEGER NOT NULL DEFAULT 0,
    start_at  INTEGER NOT NULL DEFAULT 0,
    status    TEXT NOT NULL DEFAULT 'unknown',
    url       TEXT NOT NULL DEFAULT '',
    source    TEXT NOT NULL DEFAULT '',
    fetched_at INTEGER NOT NULL
);
CREATE TABLE kv (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
INSERT INTO tasks (id, type, title, course, deadline, start_at, status, url, source, fetched_at)
VALUES ('old-task', 'homework', '旧任务', '旧课程', 0, 0, 'pending', 'https://example.test', 'legacy', 1);
INSERT INTO pushes (key, task_id, task_title, kind, offset_min, fire_at, sent_at, channel, status, error)
VALUES ('old-push', 'old-task', '旧任务', 'before_deadline', 15, 10, 11, 'test', 'sent', '');
INSERT INTO kv (key, value) VALUES ('legacy-key', 'legacy-value');
`
	if _, err := db.ExecContext(ctx, legacySchema); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	repo, err := Open(path)
	if err != nil {
		t.Fatalf("旧库迁移失败: %v", err)
	}
	snap, err := repo.LoadTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Tasks) != 1 || snap.Tasks[0].ID != "old-task" || snap.Tasks[0].Title != "旧任务" {
		t.Fatalf("迁移后旧任务丢失或失真: %+v", snap.Tasks)
	}
	if snap.Tasks[0].Score != nil || snap.Tasks[0].TotalScore != nil {
		t.Errorf("旧任务新增分数应默认为 NULL: score=%v total=%v", snap.Tasks[0].Score, snap.Tasks[0].TotalScore)
	}
	records, err := repo.RecentPushes(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Key != "old-push" {
		t.Fatalf("迁移后推送记录丢失: %+v", records)
	}
	if value, err := repo.GetKV(ctx, "legacy-key"); err != nil || value != "legacy-value" {
		t.Errorf("迁移后 kv 丢失: value=%q err=%v", value, err)
	}

	score, total := 0.0, 5.0
	if err := repo.SaveTasks(ctx, []domain.Task{{
		ID: "old-task", Type: domain.TaskHomework, Title: "旧任务", Course: "旧课程",
		Status: domain.StatusDone, URL: "https://example.test", Source: "legacy",
		Score: &score, TotalScore: &total,
	}}); err != nil {
		t.Fatalf("迁移后写入分数失败: %v", err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}

	repo2, err := Open(path)
	if err != nil {
		t.Fatalf("二次 Open 应幂等: %v", err)
	}
	defer repo2.Close()
	snap, err = repo2.LoadTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Tasks) != 1 || snap.Tasks[0].Score == nil || *snap.Tasks[0].Score != 0 ||
		snap.Tasks[0].TotalScore == nil || *snap.Tasks[0].TotalScore != 5 {
		t.Fatalf("二次 Open 后分数未保留: %+v", snap.Tasks)
	}
	records, err = repo2.RecentPushes(ctx, 10)
	if err != nil || len(records) != 1 || records[0].Key != "old-push" {
		t.Errorf("二次 Open 后推送记录丢失: records=%+v err=%v", records, err)
	}
}

func TestKV(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	v, err := repo.GetKV(ctx, "missing")
	if err != nil {
		t.Fatalf("不存在的键不应报错: %v", err)
	}
	if v != "" {
		t.Errorf("不存在的键应返回空串，实际 %q", v)
	}

	if err := repo.SetKV(ctx, "k", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetKV(ctx, "k", "v2"); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.GetKV(ctx, "k")
	if got != "v2" {
		t.Errorf("应覆盖为 v2，实际 %q", got)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.db"
	ctx := context.Background()

	repo, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Claim(ctx, "k1"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Record(ctx, PushedRecord{Key: "k1", Status: "sent", FireAt: time.Now(), SentAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	_ = repo.Close()

	repo2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo2.Close()

	keys, err := repo2.PushedKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !keys["k1"] {
		t.Error("重开数据库后已发送记录应保留（防止重启后重复推送）")
	}
}
