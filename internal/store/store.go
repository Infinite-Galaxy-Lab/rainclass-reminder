// Package store 是持久化层。对外只暴露 Repository 接口，
// SQLite 只是其中一种实现，便于测试时替换为内存实现。
package store

import (
	"context"
	"time"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/domain"
)

// PushedRecord 记录一条已发送/已处理的提醒。
type PushedRecord struct {
	Key       string    `json:"key"`
	TaskID    string    `json:"task_id"`
	TaskTitle string    `json:"task_title"`
	Kind      string    `json:"kind"`
	Offset    int       `json:"offset_min"`
	FireAt    time.Time `json:"fire_at"`
	SentAt    time.Time `json:"sent_at"`
	Channel   string    `json:"channel"`
	Status    string    `json:"status"` // sent / failed / suppressed
	Error     string    `json:"error"`
}

// TaskSnapshot 保存最近一次成功抓取的任务列表，供 Web UI 在接口失败时兜底展示。
type TaskSnapshot struct {
	FetchedAt time.Time
	Tasks     []domain.Task
}

// Repository 是调度器需要的最小持久化能力。
//
// 设计原则：接口只描述"必须持久化的状态"，不暴露 SQL 细节，
// 也不包含任何业务判断（判断在 domain 层）。
type Repository interface {
	// PushedKeys 返回所有已成功发送过的提醒键，用于 Decide 的 pending 过滤。
	PushedKeys(ctx context.Context) (map[string]bool, error)
	// Claim 原子地占用一个提醒键。返回 false 表示已被其它进程/上一轮占用，
	// 调用方应跳过发送（防止重复推送）。
	Claim(ctx context.Context, key string) (bool, error)
	// Record 写入发送结果（成功或失败）。
	Record(ctx context.Context, rec PushedRecord) error
	// Release 在发送失败且希望下轮重试时释放占用。
	Release(ctx context.Context, key string) error
	// RecentPushes 返回最近 N 条发送记录，供 Web UI 展示。
	RecentPushes(ctx context.Context, limit int) ([]PushedRecord, error)

	// SaveTasks 保存本轮任务快照。
	SaveTasks(ctx context.Context, tasks []domain.Task) error
	// LoadTasks 读取最近一次任务快照。
	LoadTasks(ctx context.Context) (TaskSnapshot, error)

	// GetKV / SetKV 存取小状态（如"登录失效已告警"标记）。
	GetKV(ctx context.Context, key string) (string, error)
	SetKV(ctx context.Context, key, value string) error

	Close() error
}
