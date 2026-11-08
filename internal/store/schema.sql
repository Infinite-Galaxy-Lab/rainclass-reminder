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
