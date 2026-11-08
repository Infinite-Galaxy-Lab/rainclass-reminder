package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/2963609262/ykt-reminder/internal/domain"
	"github.com/2963609262/ykt-reminder/internal/ykt"
)

// YktSource 把 ykt.Client 适配为调度器需要的 Fetcher。
//
// 这层适配的价值：scheduler 不依赖 ykt 包，测试时可替换为假数据源；
// 同时把"登录失效"这一 ykt 特有错误转换为调度器可识别的语义。
type YktSource struct {
	Client     *ykt.Client
	DetailDays int
	Logger     *slog.Logger
}

// FetchTasks 实现 Fetcher。
func (s *YktSource) FetchTasks(ctx context.Context) ([]domain.Task, error) {
	if s.Client == nil {
		return nil, ykt.ErrUpstream
	}
	days := s.DetailDays
	if days <= 0 {
		days = 7
	}
	tasks, stats, err := s.Client.FetchAll(ctx, days)
	if s.Logger != nil {
		s.Logger.Debug("抓取统计",
			"courses", stats.Courses,
			"leaves", stats.Leaves,
			"detail_fetched", stats.DetailsFetched,
			"detail_cached", stats.DetailsCached,
			"detail_skipped", stats.DetailsSkipped,
			"errors", stats.Errors,
		)
	}
	return tasks, err
}

// IsAuthExpired 判断抓取错误是否由登录失效引起。
func IsAuthExpired(err error) bool {
	return ykt.IsAuthError(err)
}

// 编译期断言：确保 YktSource 满足 Fetcher。
var _ Fetcher = (*YktSource)(nil)

// fakeClock 便于测试构造固定时间。
func fakeClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}
