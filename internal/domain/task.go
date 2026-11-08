// Package domain 是与外部世界无关的纯领域层。
//
// 本包刻意不 import 任何其他 internal 包，也不做任何 I/O。
// 所有函数都是纯函数，因此可以在不联网、不落盘的情况下完整单测。
package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// TaskType 是雨课堂任务的归一化类型。
//
// 雨课堂原始接口用 leaf_type 数字区分节点类型，但数字含义会随版本变化，
// 因此这一层只保留业务语义；数字到语义的映射留在 internal/ykt/mapper.go，
// 便于接口变更时只改一处。
type TaskType string

const (
	TaskHomework TaskType = "homework" // 习题/作业
	TaskExam     TaskType = "exam"     // 考试
	TaskDiscuss  TaskType = "discuss"  // 讨论
	TaskClass    TaskType = "class"    // 课堂/直播
	TaskVideo    TaskType = "video"    // 视频/课件
	TaskOther    TaskType = "other"    // 未知类型，仅展示不提醒
)

// Label 返回中文显示名。
func (t TaskType) Label() string {
	switch t {
	case TaskHomework:
		return "作业"
	case TaskExam:
		return "考试"
	case TaskDiscuss:
		return "讨论"
	case TaskClass:
		return "课堂"
	case TaskVideo:
		return "课件"
	default:
		return "任务"
	}
}

// HasDeadline 表示该类型是否天然存在截止时间概念。
// 课堂类通常只有开课时间，因此单独走开课提醒。
//
// TaskVideo 刻意排除：雨课堂给这类节点填的 score_deadline 是整门课程的
// 结束时间，若按它提醒，期末会一次性涌出上百条无意义通知。
func (t TaskType) HasDeadline() bool {
	switch t {
	case TaskHomework, TaskExam, TaskDiscuss:
		return true
	}
	return false
}

// TaskStatus 表示任务完成状态。
type TaskStatus string

const (
	StatusUnknown TaskStatus = "unknown" // 接口未暴露状态
	StatusPending TaskStatus = "pending" // 未完成
	StatusDone    TaskStatus = "done"    // 已完成
	StatusExpired TaskStatus = "expired" // 已过截止
)

// Task 是一个归一化后的待办任务。它是整个系统内部唯一流通的任务表示，
// API DTO 只能通过 ykt/mapper 转成它，不允许上层直接接触原始 JSON。
type Task struct {
	ID         string     // 稳定唯一键（跨轮次不变），用于去重与持久化
	Type       TaskType   // 归一化类型
	Title      string     // 任务标题
	Course     string     // 课程名
	Deadline   time.Time  // 截止时间；零值表示无截止
	StartAt    time.Time  // 开课/开始时间；零值表示无
	Status     TaskStatus // 完成状态
	Score      *float64   // 可选得分；nil 表示接口未提供
	TotalScore *float64   // 可选总分；nil 表示接口未提供
	URL        string     // 在网页端的直达链接
	Source     string     // 数据来源标识（如 "chapter:leaf"），便于排障
}

// Validate 做最小一致性检查：ID 与标题必须存在。
func (t Task) Validate() error {
	if strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("task id 为空")
	}
	if strings.TrimSpace(t.Title) == "" {
		return fmt.Errorf("task %s 标题为空", t.ID)
	}
	return nil
}

// HasDeadline 判断任务是否带可用截止时间。
func (t Task) HasDeadline() bool { return !t.Deadline.IsZero() }

// IsExpired 判断任务在给定时刻是否已过截止。
func (t Task) IsExpired(now time.Time) bool {
	return t.HasDeadline() && !now.Before(t.Deadline)
}

// TimeLeft 返回距截止的剩余时间（可能为负）。
func (t Task) TimeLeft(now time.Time) time.Duration {
	if !t.HasDeadline() {
		return 0
	}
	return t.Deadline.Sub(now)
}

// Remaining 返回人类可读的剩余时间，例如 "3天2小时"。
// 负数表示已过期。
func (t Task) Remaining(now time.Time) string {
	if !t.HasDeadline() {
		return "无截止"
	}
	d := t.Deadline.Sub(now)
	if d <= 0 {
		return "已过期 " + HumanDuration(-d)
	}
	return HumanDuration(d)
}

// HumanDuration 把时长格式化成紧凑中文："2天3小时"、"45分钟"、"30秒"。
func HumanDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	if d < time.Minute {
		return fmt.Sprintf("%d秒", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d分钟", int(d.Minutes()))
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%d天%d小时", days, hours)
	case days > 0:
		return fmt.Sprintf("%d天", days)
	case minutes > 0 && minutes < 60 && hours > 0:
		return fmt.Sprintf("%d小时%d分钟", hours, minutes)
	default:
		return fmt.Sprintf("%d小时", hours)
	}
}

// SortTasks 按"有截止的在前、截止早的在前"稳定排序，无截止的排最后。
func SortTasks(tasks []Task) {
	sort.SliceStable(tasks, func(i, j int) bool {
		a, b := tasks[i], tasks[j]
		if a.HasDeadline() != b.HasDeadline() {
			return a.HasDeadline()
		}
		if a.HasDeadline() && !a.Deadline.Equal(b.Deadline) {
			return a.Deadline.Before(b.Deadline)
		}
		return a.Title < b.Title
	})
}
