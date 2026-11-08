// Package notify 是推送层的抽象。
//
// 上层（scheduler）只依赖 Notifier 接口，具体通道在 registry 中注册；
// 新增通道（邮件/QQ/Telegram）只需实现接口并注册，不改动调度逻辑。
package notify

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Message 是一条待推送的通知（通道无关的中间表示）。
type Message struct {
	Title    string
	Markdown string
	// Urgent 表示高优先级（如已过期/即将截止），通道可据此选择更强提醒。
	Urgent bool
	// Level 是语义级别，用于标题前缀：info / warn / urgent。
	Level string
}

// Notifier 是推送通道接口。
type Notifier interface {
	// Name 返回通道标识（用于日志与去重记录）。
	Name() string
	// Send 发送一条消息。返回的错误会被记录并在下一轮重试。
	Send(ctx context.Context, msg Message) error
}

// Registry 管理已启用的通道。
type Registry struct {
	channels []Notifier
}

// NewRegistry 按名字顺序构造通道集合。
//
// 未识别的名字会被忽略（记录在返回的警告里），
// 这样配置文件里预留未来通道不会导致启动失败。
func NewRegistry(enabled []string, factories map[string]func() (Notifier, error)) (*Registry, []string, error) {
	var (
		out  []Notifier
		warn []string
	)
	for _, name := range enabled {
		name = strings.TrimSpace(strings.ToLower(name))
		if name == "" {
			continue
		}
		f, ok := factories[name]
		if !ok {
			warn = append(warn, fmt.Sprintf("未知通知通道 %q，已跳过", name))
			continue
		}
		n, err := f()
		if err != nil {
			warn = append(warn, fmt.Sprintf("通道 %q 初始化失败: %v", name, err))
			continue
		}
		if n == nil {
			warn = append(warn, fmt.Sprintf("通道 %q 未配置，已跳过", name))
			continue
		}
		out = append(out, n)
	}
	return &Registry{channels: out}, warn, nil
}

// Names 返回已启用通道名。
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.channels))
	for _, c := range r.channels {
		out = append(out, c.Name())
	}
	sort.Strings(out)
	return out
}

// Empty 报告是否没有任何可用通道。
func (r *Registry) Empty() bool { return r == nil || len(r.channels) == 0 }

// SendResult 描述一次发送的结果。
type SendResult struct {
	Channel string
	Err     error
}

// Send 依次尝试所有通道，任一成功即返回成功。
//
// 语义：宁可重复也不漏发——因此只要有一个通道成功，其余失败不算整体失败，
// 但所有结果都会返回给调用方记录。
func (r *Registry) Send(ctx context.Context, msg Message) ([]SendResult, error) {
	if ctx == nil {
		// 防御：调用方误传 nil 会导致下游 panic，这里兜底为背景上下文。
		ctx = context.Background()
	}
	if r.Empty() {
		return nil, fmt.Errorf("没有可用的通知通道")
	}
	results := make([]SendResult, 0, len(r.channels))
	var lastErr error
	success := false
	for _, c := range r.channels {
		// 尊重调用方超时，但单个通道失败不阻断后续通道。
		if err := ctx.Err(); err != nil {
			results = append(results, SendResult{Channel: c.Name(), Err: err})
			lastErr = err
			continue
		}
		err := c.Send(ctx, msg)
		results = append(results, SendResult{Channel: c.Name(), Err: err})
		if err == nil {
			success = true
		} else {
			lastErr = err
		}
	}
	if success {
		return results, nil
	}
	return results, lastErr
}

// LevelFor 根据紧急程度决定消息级别。
func LevelFor(urgent bool, expired bool) string {
	switch {
	case expired:
		return "urgent"
	case urgent:
		return "warn"
	default:
		return "info"
	}
}

// Now 供测试替换的时间源。
var Now = time.Now
