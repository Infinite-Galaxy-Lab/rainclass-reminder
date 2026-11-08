// Package ykt 是雨课堂 Web API 的只读客户端。
//
// 设计约束（重要）：
//   - 本包只允许 GET 列表/详情类请求，绝不提供提交、答题、交卷等方法；
//   - 所有 URL 常量集中在 endpoints.go，接口变更时只改一处；
//   - 上层只接触 domain.Task，不接触任何原始 JSON。
package ykt

import "errors"

// 错误分类。上层据此决定"重试 / 重新登录 / 跳过"。
var (
	// ErrAuthExpired 表示登录态失效（401/302/被重定向到登录页）。
	ErrAuthExpired = errors.New("登录态已失效")
	// ErrRateLimited 表示被限流（429）。
	ErrRateLimited = errors.New("请求被限流")
	// ErrNotFound 表示资源不存在（例如作业已被删除）。
	ErrNotFound = errors.New("资源不存在")
	// ErrUpstream 表示服务端错误或不可解析响应。
	ErrUpstream = errors.New("上游返回异常")
)

// IsAuthError 判断错误是否属于"需要重新登录"。
func IsAuthError(err error) bool { return errors.Is(err, ErrAuthExpired) }

// IsTransient 判断是否值得下一轮重试（限流、5xx）。
func IsTransient(err error) bool {
	return errors.Is(err, ErrRateLimited) || errors.Is(err, ErrUpstream)
}
