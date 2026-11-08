package ykt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/domain"
)

// 本文件实现各只读接口的调用，返回已归一化的数据。

// BasicInfo 校验登录态并返回用户信息。
func (c *Client) BasicInfo(ctx context.Context) (info BasicInfoData, resultErr error) {
	if c.observer != nil {
		defer func() { c.observer.AuthResult(time.Now(), observationOutcome(resultErr)) }()
	}
	var env APIEnvelope[BasicInfoData]
	if err := c.getJSON(ctx, pathUserBasicInfo, nil, &env); err != nil {
		return BasicInfoData{}, err
	}
	if err := envelopeErr(env); err != nil {
		return BasicInfoData{}, err
	}
	return env.Data, nil
}

// CheckAuth 做一次轻量登录态校验，返回错误可直接用 IsAuthError 判断。
func (c *Client) CheckAuth(ctx context.Context) error {
	_, err := c.BasicInfo(ctx)
	return err
}

// Courses 返回学生视角的全部课程。
func (c *Client) Courses(ctx context.Context) (courses []CourseItem, resultErr error) {
	if c.observer != nil {
		defer func() { c.observer.AuthResult(time.Now(), observationOutcome(resultErr)) }()
	}
	q := url.Values{}
	q.Set("identity", fmt.Sprintf("%d", c.identity))

	var env APIEnvelope[CourseListData]
	if err := c.getJSON(ctx, pathCourseList, q, &env); err != nil {
		return nil, err
	}
	if err := envelopeErr(env); err != nil {
		return nil, err
	}
	return env.Data.Items(), nil
}

// Chapter 返回某课程的章节树（带 TTL 缓存）。
func (c *Client) Chapter(ctx context.Context, classroomID string) (*ChapterData, error) {
	c.chapterMu.Lock()
	if e, ok := c.chapterCache[classroomID]; ok && time.Since(e.at) < c.chapterTTL {
		c.chapterMu.Unlock()
		return e.data, nil
	}
	c.chapterMu.Unlock()

	q := url.Values{}
	q.Set("cid", classroomID)
	q.Set("classroom_id", classroomID)

	var env APIEnvelope[ChapterData]
	if err := c.getJSON(ctx, pathChapter, q, &env); err != nil {
		return nil, err
	}
	if err := envelopeErr(env); err != nil {
		return nil, err
	}

	c.chapterMu.Lock()
	c.chapterCache[classroomID] = chapterCacheEntry{at: time.Now(), data: &env.Data}
	c.chapterMu.Unlock()
	return &env.Data, nil
}

// LeafInfo 返回某叶子的详情（带 TTL 缓存）。
//
// leafID 必须是章节树节点 id，不是 leafinfo_id——传后者会返回 404000。
func (c *Client) LeafInfo(ctx context.Context, classroomID, leafID string) (LeafInfoData, error) {
	data, _, err := c.leafInfoCached(ctx, classroomID, leafID, time.Time{})
	return data, err
}

// leafInfoCached 是带缓存标记的详情查询。
//
// deadlineHint 决定缓存时长：越临近截止刷得越勤，远期任务缓存数小时。
// 第二个返回值表示结果是否来自缓存，供调用方统计真实请求量。
func (c *Client) leafInfoCached(ctx context.Context, classroomID, leafID string, deadlineHint time.Time) (LeafInfoData, bool, error) {
	cacheKey := classroomID + ":" + leafID
	ttl := c.ttlFor(deadlineHint)

	c.detailMu.Lock()
	if e, ok := c.detailCache[cacheKey]; ok && time.Since(e.at) < ttl {
		c.detailMu.Unlock()
		return e.data, true, nil
	}
	c.detailMu.Unlock()

	path := fmt.Sprintf(pathLeafInfo, url.PathEscape(classroomID), url.PathEscape(leafID))

	var env APIEnvelope[LeafInfoData]
	// classroom-id 头缺失时服务端仍返回 HTTP 200，只在 error_code 里报错。
	if err := c.getJSONScoped(ctx, path, nil, classroomID, &env); err != nil {
		return LeafInfoData{}, false, err
	}
	if err := envelopeErr(env); err != nil {
		return LeafInfoData{}, false, err
	}

	c.detailMu.Lock()
	c.detailCache[cacheKey] = detailCacheEntry{at: time.Now(), data: env.Data}
	c.detailMu.Unlock()
	return env.Data, false, nil
}

// learnLogsCached 拉取某门课的学习日志（带 TTL 缓存）。
//
// 按 offset 翻页，has_more 为 true 时继续。maxPages 防止异常数据导致死循环。
func (c *Client) learnLogsCached(ctx context.Context, classroomID string) ([]LearnLogActivity, bool, error) {
	cacheKey := "logs:" + classroomID

	c.logMu.Lock()
	if e, ok := c.logCache[cacheKey]; ok && time.Since(e.at) < c.logTTL {
		c.logMu.Unlock()
		return e.data, true, nil
	}
	c.logMu.Unlock()

	path := fmt.Sprintf(pathLearnLogs, url.PathEscape(classroomID))

	const (
		maxPages = 10
		pageSize = 20
	)
	var all []LearnLogActivity
	for page := 0; page < maxPages; page++ {
		q := url.Values{}
		// actype=-1 = 全部。实测 actype=100 返回空列表。
		q.Set("actype", "-1")
		q.Set("page", fmt.Sprintf("%d", page))
		q.Set("offset", fmt.Sprintf("%d", pageSize))
		q.Set("sort", "-1")

		var env APIEnvelope[LearnLogData]
		if err := c.getJSON(ctx, path, q, &env); err != nil {
			if len(all) > 0 {
				break // 已经拿到一部分，不为后续页失败整体报错
			}
			return nil, false, err
		}
		if err := envelopeErr(env); err != nil {
			if len(all) > 0 {
				break
			}
			return nil, false, err
		}

		all = append(all, env.Data.Activities...)
		if !env.Data.HasMore || len(env.Data.Activities) == 0 {
			break
		}
	}

	c.logMu.Lock()
	c.logCache[cacheKey] = logCacheEntry{at: time.Now(), data: all}
	c.logMu.Unlock()
	return all, false, nil
}

// LearnLogs 是 learnLogsCached 的导出版本（probe 与测试用）。
func (c *Client) LearnLogs(ctx context.Context, classroomID string) ([]LearnLogActivity, error) {
	logs, _, err := c.learnLogsCached(ctx, classroomID)
	return logs, err
}

// LearnLogsDebug 返回学习日志与是否命中缓存（probe 用）。
func (c *Client) LearnLogsDebug(ctx context.Context, classroomID string) ([]LearnLogActivity, bool, error) {
	return c.learnLogsCached(ctx, classroomID)
}

// ClearLearnLogCache 清空学习日志缓存（测试与排查用）。
func (c *Client) ClearLearnLogCache() {
	c.logMu.Lock()
	defer c.logMu.Unlock()
	c.logCache = make(map[string]logCacheEntry)
}

// ExamCover 返回考试封面信息。
func (c *Client) ExamCover(ctx context.Context, examID, classroomID string) (ExamCoverData, error) {
	q := url.Values{}
	q.Set("exam_id", examID)
	q.Set("classroom_id", classroomID)

	var env APIEnvelope[ExamCoverData]
	if err := c.getJSON(ctx, pathExamCover, q, &env); err != nil {
		return ExamCoverData{}, err
	}
	if err := envelopeErr(env); err != nil {
		return ExamCoverData{}, err
	}
	return env.Data, nil
}

// FetchStats 记录一轮抓取的统计，便于日志与排障。
type FetchStats struct {
	Courses int
	Leaves  int
	// DetailsFetched 是真实发出的详情 HTTP 请求数（不含缓存命中）。
	DetailsFetched int
	// DetailsCached 是详情缓存命中数——省下的请求。
	DetailsCached  int
	DetailsSkipped int
	// LogsFetched / LogsCached 是学习日志的请求数与缓存命中数。
	LogsFetched int
	LogsCached  int
	// LinksFromLogs 是有多少任务的链接来自学习日志的真实 courseware_id。
	// 这个数字低说明匹配没成功，需要看日志标题是否与章节树一致。
	LinksFromLogs int
	// StatusFromLogs 是有多少任务的完成状态来自学习日志的 status 字段。
	// 详情接口不提供完成状态，因此这个数字应当接近"作业+考试"的总数。
	StatusFromLogs int
	Errors         int
}

// CourseMetaUserInfo 触达 /v/course_meta/user_info。
//
// 这个接口本身不返回我们需要的业务数据，但它是全站唯一会下发
// Set-Cookie 的端点（实测 2026-11：每次响应都带新的 csrftoken）。
// 每轮抓取开始时调用一次，有两个作用：
//  1. 让 CookieObserver 拿到新鲜的 csrftoken；
//  2. 作为"这一轮确实连通了"的探针——它的响应最快，能最早暴露登录失效。
//
// 失败不视为致命：业务请求自己也会给出明确的登录失效错误。
func (c *Client) CourseMetaUserInfo(ctx context.Context) error {
	var raw json.RawMessage
	if err := c.getJSON(ctx, pathCourseMetaUserInfo, nil, &raw); err != nil {
		return err
	}
	return nil
}

// FetchAll 抓取全部课程的任务并归一化为 domain.Task。
//
// 抓取策略（控制请求量）：
//  1. 拉课程列表；
//  2. 并发拉各课程章节树（带缓存）；
//  3. 只对 detailDays 天内到期、或已过期的叶子拉详情——
//     远期任务的截止时间在章节树里已经够用，无需详情。
//
// 单个课程失败不影响其它课程（返回部分结果 + 错误聚合）。
func (c *Client) FetchAll(ctx context.Context, detailDays int) ([]domain.Task, FetchStats, error) {
	if detailDays <= 0 {
		detailDays = 7
	}
	stats := FetchStats{}

	// 先用轻量端点做一次会话触达：刷新 csrftoken，并尽早发现登录失效。
	// 错误在这里吞掉——真正的错误会由下面的 Courses 更准确地报出。
	if err := c.CourseMetaUserInfo(ctx); err != nil {
		c.log.Debug("会话触达失败（不影响抓取）", "err", err)
	}

	courses, err := c.Courses(ctx)
	if err != nil {
		return nil, stats, err
	}
	stats.Courses = len(courses)

	type courseResult struct {
		tasks []domain.Task
		stats FetchStats
		err   error
	}

	results := make(chan courseResult, len(courses))
	for _, course := range courses {
		course := course
		go func() {
			tasks, st, err := c.fetchCourse(ctx, course, detailDays)
			results <- courseResult{tasks: tasks, stats: st, err: err}
		}()
	}

	var (
		all  []domain.Task
		errs []error
	)
	for range courses {
		r := <-results
		all = append(all, r.tasks...)
		stats.Leaves += r.stats.Leaves
		stats.DetailsFetched += r.stats.DetailsFetched
		stats.DetailsCached += r.stats.DetailsCached
		stats.DetailsSkipped += r.stats.DetailsSkipped
		stats.LogsFetched += r.stats.LogsFetched
		stats.LogsCached += r.stats.LogsCached
		stats.LinksFromLogs += r.stats.LinksFromLogs
		stats.StatusFromLogs += r.stats.StatusFromLogs
		stats.Errors += r.stats.Errors
		if r.err != nil {
			errs = append(errs, r.err)
		}
	}

	// 去重（同一叶子可能在树里出现多次）。
	all = dedupeTasks(all)
	domain.SortTasks(all)

	if len(errs) > 0 {
		return all, stats, fmt.Errorf("部分课程抓取失败（%d 个）: %w", len(errs), errs[0])
	}
	return all, stats, nil
}

// fetchCourse 抓取单门课程的任务。
func (c *Client) fetchCourse(ctx context.Context, course CourseItem, detailDays int) ([]domain.Task, FetchStats, error) {
	stats := FetchStats{}
	classroomID := course.ClassroomID.String()
	if classroomID == "" {
		return nil, stats, fmt.Errorf("课程 %q 缺少 classroom_id", course.DisplayName())
	}
	courseName := course.DisplayName()
	baseURL := c.baseURL

	data, err := c.Chapter(ctx, classroomID)
	if err != nil {
		return nil, stats, fmt.Errorf("课程 %q 章节抓取失败: %w", courseName, err)
	}

	// 真实结构在 course_chapter；兼容 chapters 历史字段。
	root := data.CourseChapter
	if len(root) == 0 {
		root = data.Chapters
	}

	// 课程名优先用接口返回的 course_name（比列表里的 name 更完整）。
	if n := strings.TrimSpace(data.CourseName.String()); n != "" {
		courseName = n
	}

	leaves := collectLeaves(root, courseName, classroomID)
	leaves = append(leaves, collectFlatLeaves(data.LeafInfo, courseName, classroomID)...)
	stats.Leaves = len(leaves)

	// 学习日志用来补全考试/习题的真实 ID：章节树的 exam.exam_id 全站为空，
	// 只有学习日志能给出试卷 ID。纯视频课没有试卷叶子，不必拉。
	var cwIndex *coursewareIndex
	if needsCoursewareID(leaves) {
		cwIndex = c.buildCoursewareIndex(ctx, classroomID, &stats)
	}

	deadlineHorizon := time.Now().Add(time.Duration(detailDays) * 24 * time.Hour)
	tasks := make([]domain.Task, 0, len(leaves))
	for _, leaf := range leaves {
		task := toTask(leaf, baseURL)

		// 按"拉回来有没有用"决定是否拉详情。纯内容类占全部叶子的半壁江山，
		// 跳过它们能省掉大量请求。
		needDetail := true
		switch {
		case isPureContentLeaf(leaf):
			needDetail = false
		case leaf.deadline == 0:
			// 章节树没给截止时间，详情可能给。
			needDetail = true
		case timeOrZero(leaf.deadline).Before(deadlineHorizon):
			// 临近或已过期，要新鲜数据。
			needDetail = true
		default:
			// 远期且已知截止时间：章节树的信息足够排期。
			needDetail = false
		}

		if needDetail {
			// 详情接口要的是章节树节点 id，不是 leafinfo_id。
			// deadlineHint 让缓存 TTL 按紧迫程度自适应：远期任务几乎不重拉。
			info, cached, err := c.leafInfoCached(ctx, classroomID, leaf.leafID, timeOrZero(leaf.deadline))
			if err != nil {
				if IsAuthError(err) {
					return nil, stats, err // 登录态问题应立即上报
				}
				// 404 常见（叶子已删除或无权限），不算异常。
				if !errors.Is(err, ErrNotFound) {
					stats.Errors++
				}
			} else {
				if cached {
					stats.DetailsCached++
				} else {
					stats.DetailsFetched++
				}
				task = applyLeafInfo(task, info)
				// 详情可能改写标题并给出服务端的内容类型标签，
				// 链接要在详情之后按标签重算一次。
				task.URL = leafURLWithLabel(c.baseURL, leaf, info.ContentLabel())
			}
		} else {
			stats.DetailsSkipped++
		}

		if task.Title == "" {
			task.Title = "未命名" + task.Type.Label()
		}

		// 用学习日志里的 courseware_id 覆盖链接。必须放在标题定型之后，
		// 因为匹配是按标题做的。
		//
		// 只有试卷类（leaf_type 5/6/9）允许被老版路由覆盖：课件/视频/讨论
		// 即使标题撞上日志条目也必须留在章节路由里，否则会生成打不开的链接。
		if cwIndex != nil && isPaperLeaf(leaf.leafType) {
			if cw, ok := cwIndex.lookup(task.Title); ok {
				task.Score = cw.score.Ptr()
				task.TotalScore = cw.totalScore.Ptr()
				if u := cw.webURL(baseURL, classroomID); u != "" {
					task.URL = u
					stats.LinksFromLogs++
				}
				// 完成状态：详情接口没有 is_finish/status 字段，
				// 学习日志的 status 是唯一可靠来源。
				if task.Status == domain.StatusUnknown {
					if st := learnLogStatus(cw.status, cw.statusKnown); st != domain.StatusUnknown {
						task.Status = st
						stats.StatusFromLogs++
					}
				}
			}
		}

		if err := task.Validate(); err != nil {
			stats.Errors++
			continue
		}
		tasks = append(tasks, task)
	}
	return tasks, stats, nil
}

// LeafTypeCounts 统计章节树里各 leaf_type 的出现次数（probe 用）。
//
// 用途：接口类型编码变化时，一眼看出新增/消失的类型，
// 便于及时更新 leafTypeMap。
func LeafTypeCounts(data *ChapterData) map[int]int {
	counts := make(map[int]int)
	for _, c := range collectLeaves(chapterRoot(data), "", "") {
		counts[int(c.leafType)]++
	}
	for _, l := range data.LeafInfo {
		counts[int(l.LeafType.Int64())]++
	}
	return counts
}

// CountLeaves 返回章节树中的叶子总数。
func CountLeaves(data *ChapterData) int {
	return len(collectLeaves(chapterRoot(data), "", "")) + len(data.LeafInfo)
}

// chapterRoot 返回章节树的根节点列表，兼容真实结构与历史结构。
func chapterRoot(data *ChapterData) []ChapterNode {
	if len(data.CourseChapter) > 0 {
		return data.CourseChapter
	}
	return data.Chapters
}

// dedupeTasks 按 ID 去重，保留信息更完整的一条（截止时间优先）。
func dedupeTasks(tasks []domain.Task) []domain.Task {
	seen := make(map[string]int, len(tasks))
	out := make([]domain.Task, 0, len(tasks))
	for _, t := range tasks {
		if idx, ok := seen[t.ID]; ok {
			// 已有：用"信息更多"的覆盖
			if len(t.Title) > len(out[idx].Title) || (!t.Deadline.IsZero() && out[idx].Deadline.IsZero()) {
				out[idx] = t
			}
			continue
		}
		seen[t.ID] = len(out)
		out = append(out, t)
	}
	return out
}
