package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/infinite-galaxy/rainclass-reminder/internal/domain"
	_ "modernc.org/sqlite"
)

// schemaSQL 为建表脚本（纯 DDL，无外部输入）。
const schemaSQL = `
CREATE TABLE IF NOT EXISTS pushes (
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
CREATE INDEX IF NOT EXISTS idx_pushes_sent_at ON pushes(sent_at DESC);
CREATE TABLE IF NOT EXISTS tasks (
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
CREATE TABLE IF NOT EXISTS kv (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`

// openSchema 返回包内固定的建表语句，确保只有一处来源。
func openSchema() string { return schemaSQL }

// SQLite 是 Repository 的 SQLite 实现（纯 Go 驱动，无需 cgo）。
type SQLite struct {
	db *sql.DB
}

// Open 打开（必要时创建）数据库并完成建表。
func Open(path string) (*SQLite, error) {
	if path != ":memory:" {
		path = filepath.Clean(path)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("创建数据库目录失败: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.ExecContext(context.Background(), `PRAGMA busy_timeout = 5000`); err != nil {
		db.Close()
		return nil, fmt.Errorf("设置 busy_timeout 失败: %w", err)
	}
	if _, err := db.ExecContext(context.Background(), `PRAGMA journal_mode = WAL`); err != nil {
		db.Close()
		return nil, fmt.Errorf("设置 journal_mode 失败: %w", err)
	}
	if _, err := db.ExecContext(context.Background(), openSchema()); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化表结构失败: %w", err)
	}
	return &SQLite{db: db}, nil
}

func (s *SQLite) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// PushedKeys 返回所有已发送或已抑制的提醒键。
func (s *SQLite) PushedKeys(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key FROM pushes WHERE status IN ('sent', 'suppressed')`)
	if err != nil {
		return nil, fmt.Errorf("查询已推送键失败: %w", err)
	}
	defer rows.Close()

	out := make(map[string]bool)
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}

// Claim 尝试占用一个提醒键，返回是否取得发送权。
//
// 语义：
//   - 键不存在 → 插入 claimed 占位，取得发送权；
//   - 键已存在且状态为 failed → 接管为 claimed（允许下一轮重试）；
//   - 键已存在且状态为 sent/suppressed/claimed → 不取得（避免重复推送）。
//
// ON CONFLICT ... DO UPDATE ... WHERE 让"判断 + 占位"保持为一条原子语句，
// 避免多实例并发时的重复推送。
func (s *SQLite) Claim(ctx context.Context, key string) (bool, error) {
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx, `
INSERT INTO pushes (key, task_id, task_title, kind, offset_min, fire_at, sent_at, channel, status, error)
VALUES (?, '', '', '', 0, ?, ?, '', 'claimed', '')
ON CONFLICT(key) DO UPDATE SET
  status = 'claimed',
  sent_at = excluded.sent_at,
  error = ''
WHERE pushes.status = 'failed'`, key, now, now)
	if err != nil {
		return false, fmt.Errorf("占用提醒键失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// Record 写入发送结果；若键已存在则更新。
func (s *SQLite) Record(ctx context.Context, rec PushedRecord) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO pushes (key, task_id, task_title, kind, offset_min, fire_at, sent_at, channel, status, error)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(key) DO UPDATE SET
  task_id = excluded.task_id,
  task_title = excluded.task_title,
  kind = excluded.kind,
  offset_min = excluded.offset_min,
  fire_at = excluded.fire_at,
  sent_at = excluded.sent_at,
  channel = excluded.channel,
  status = excluded.status,
  error = excluded.error`,
		rec.Key, rec.TaskID, rec.TaskTitle, rec.Kind, rec.Offset, rec.FireAt.Unix(),
		rec.SentAt.Unix(), rec.Channel, rec.Status, rec.Error)
	if err != nil {
		return fmt.Errorf("写入推送记录失败: %w", err)
	}
	return nil
}

// Release 删除占位记录，使下轮可以重试。
func (s *SQLite) Release(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM pushes WHERE key = ? AND status = 'claimed'`, key)
	return err
}

// RecentPushes 返回最近的推送记录。
func (s *SQLite) RecentPushes(ctx context.Context, limit int) ([]PushedRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT key, task_id, task_title, kind, offset_min, fire_at, sent_at, channel, status, error
FROM pushes WHERE status <> 'claimed' ORDER BY sent_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PushedRecord
	for rows.Next() {
		var r PushedRecord
		var fireAt, sentAt int64
		if err := rows.Scan(&r.Key, &r.TaskID, &r.TaskTitle, &r.Kind, &r.Offset,
			&fireAt, &sentAt, &r.Channel, &r.Status, &r.Error); err != nil {
			return nil, err
		}
		r.FireAt = time.Unix(fireAt, 0)
		r.SentAt = time.Unix(sentAt, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveTasks 用事务整体替换任务表，保证 UI 不会读到半新半旧的数据。
func (s *SQLite) SaveTasks(ctx context.Context, tasks []domain.Task) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM tasks`); err != nil {
		return err
	}
	pstmt, err := tx.PrepareContext(ctx, `
INSERT OR REPLACE INTO tasks (id, type, title, course, deadline, start_at, status, url, source, fetched_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer pstmt.Close()

	now := time.Now().Unix()
	for _, t := range tasks {
		if _, err := pstmt.ExecContext(ctx, t.ID, string(t.Type), t.Title, t.Course,
			unixOrZero(t.Deadline), unixOrZero(t.StartAt), string(t.Status), t.URL, t.Source, now); err != nil {
			return fmt.Errorf("写入任务 %s 失败: %w", t.ID, err)
		}
	}
	return tx.Commit()
}

// LoadTasks 读取任务快照。
func (s *SQLite) LoadTasks(ctx context.Context) (TaskSnapshot, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, type, title, course, deadline, start_at, status, url, source
FROM tasks ORDER BY (deadline = 0), deadline ASC`)
	if err != nil {
		return TaskSnapshot{}, err
	}
	defer rows.Close()

	snap := TaskSnapshot{}
	for rows.Next() {
		var t domain.Task
		var typ, status string
		var deadline, startAt int64
		if err := rows.Scan(&t.ID, &typ, &t.Title, &t.Course, &deadline, &startAt,
			&status, &t.URL, &t.Source); err != nil {
			return TaskSnapshot{}, err
		}
		t.Type = domain.TaskType(typ)
		t.Status = domain.TaskStatus(status)
		t.Deadline = timeOrZero(deadline)
		t.StartAt = timeOrZero(startAt)
		snap.Tasks = append(snap.Tasks, t)
	}
	if err := rows.Err(); err != nil {
		return TaskSnapshot{}, err
	}

	if v, err := s.GetKV(ctx, "tasks_fetched_at"); err == nil && v != "" {
		if ts, err := time.Parse(time.RFC3339, v); err == nil {
			snap.FetchedAt = ts
		}
	}
	return snap, nil
}

// GetKV 读取键值；不存在时返回空串而非错误。
func (s *SQLite) GetKV(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM kv WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// SetKV 写入键值。
func (s *SQLite) SetKV(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO kv (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// SaveSnapshotTime 记录任务抓取时间（便于 UI 显示"数据新鲜度"）。
func (s *SQLite) SaveSnapshotTime(ctx context.Context, at time.Time) error {
	return s.SetKV(ctx, "tasks_fetched_at", at.Format(time.RFC3339))
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func timeOrZero(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}
