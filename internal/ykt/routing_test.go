package ykt

import (
	"testing"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/domain"
)

// 本文件锁定"任务 → 网页端路由"的**完整分类规则**。
//
// 历史教训（v0.6.3 ~ v0.6.6，四轮修正）：
//
//	v0.6.3  以为"所有任务都走 ai-workspace"，只真机验证了 3 个类型
//	v0.6.5  发现考试/习题要用学习日志的 courseware_id 走老版页面
//	v0.6.6  发现 leaf_type=4 是讨论不是作业（用户给出真实 forum 链接）
//	v0.6.6  发现课件(3/7)与视频共用 video 阅读器，走 quiz 报"没有权限"
//
// 根因是同一个：**业务类型与路由被耦合在一起**。现在分开：
//   - 业务类型（domain.TaskType）= 展示标签 + 是否参与截止提醒
//   - 路由（routeKind + segment）= 网页端把内容挂在哪个组件下
//
// 本文件的用例全部来自真机验证（浏览器逐条打开，以"页面是否渲染出
// 真实内容"为准，不以"URL 看起来对不对"为准）。

const testBase = "https://changjiang.yuketang.cn"

// TestChapterSegmentByContent 锁定"服务端标签 → 路由段"的映射。
//
// 这是最可靠的一层：/leaf_info 的 content_info.score_evaluation.name
// 是服务端自己给的类型标签，实测比 leaf_type 和标题关键词都准。
func TestChapterSegmentByContent(t *testing.T) {
	cases := []struct {
		label    string
		leafType int64
		want     string
		why      string
	}{
		{"讨论", 6, "forum", "「讨论-导论」leaf_type=6，但服务端标「讨论」"},
		{"讨论", 0, "forum", "「课前讨论」leaf_type=0，服务端仍标「讨论」"},
		{"讨论", 4, "forum", "「小组作业」leaf_type=4，标题含「作业」但实为讨论"},
		{"作业", 5, "quiz", "普通作业"},
		{"考试", 5, "quiz", "考试先落章节，再由学习日志升级为老版页"},
		{"视频", 8, "video", "视频"},
		{"图文", 3, "video", "图文与视频共用阅读器；走 node 报「知识点不存在」"},
		{"课堂", 8, "video", "课堂录像"},
		{"雨课堂课件", 7, "video", "「作业1单元」服务端标「雨课堂课件」，走 quiz 报「没有权限」"},
	}
	for _, tc := range cases {
		got := chapterSegmentByContent(tc.label, tc.leafType)
		if got != tc.want {
			t.Errorf("chapterSegmentByContent(%q, %d) = %q, want %q（%s）",
				tc.label, tc.leafType, got, tc.want, tc.why)
		}
	}

	// 标签为空时退回 leaf_type。
	if got := chapterSegmentByContent("", 4); got != "forum" {
		t.Errorf("空标签应退回 leaf_type，得到 %q", got)
	}
	if got := chapterSegmentByContent("未知标签", 8); got != "video" {
		t.Errorf("未知标签应退回 leaf_type，得到 %q", got)
	}
}

// TestTaskTypeFromLabel 锁定"服务端标签 → 业务类型"。
//
// 业务类型只影响展示与提醒，不影响路由。两者分开后，
// 「作业1单元」可以既是"作业"（展示）又走 video 路由（能打开）。
func TestTaskTypeFromLabel(t *testing.T) {
	cases := map[string]domain.TaskType{
		"讨论":    domain.TaskDiscuss,
		"考试":    domain.TaskExam,
		"作业":    domain.TaskHomework,
		"视频":    domain.TaskVideo,
		"图文":    domain.TaskVideo,
		"课堂":    domain.TaskVideo,
		"雨课堂课件": domain.TaskVideo,
		"":      domain.TaskOther,
		"未知":    domain.TaskOther,
	}
	for label, want := range cases {
		if got := taskTypeFromLabel(label); got != want {
			t.Errorf("taskTypeFromLabel(%q) = %v, want %v", label, got, want)
		}
	}
}

// TestAssessedCoursewareBecomesHomework 锁定"计分课件必须参与提醒"。
//
// 真实漏洞（v0.6.6 发现）：leaf_type=7 的 21 个"课件"里，
// 「P1.3 电路元件」「作业1单元」「期中2026」「课程前后复习&预习」
// 全部 is_assessed=true、score_proportion=0.05、score_deadline 有值，
// 占课程成绩 5%，是需要提交且会过期的任务。
//
// 按"课件=TaskVideo"处理会让这 34 条任务完全不参与截止提醒——属于漏提醒。
//
// 判据 is_assessed && score_deadline>0 的实测分布（180 个叶子）：
//
//	leaf_type 5/7/9  出现      作业/计分课件/考试
//	leaf_type 0/3/8  从不出现   纯视频/图文/课堂录像
func TestAssessedCoursewareBecomesHomework(t *testing.T) {
	// 计分课件 → 作业（会提醒）
	assessed := LeafInfoData{
		ScoreDeadline: 1773748800,
		IsAssessed:    true,
	}
	assessed.ContentInfo.ScoreEvaluation.Name = "雨课堂课件"
	if got := taskTypeFromInfo(assessed); got != domain.TaskHomework {
		t.Errorf("计分课件应提升为作业，实际 %v", got)
	}

	// 不评分的课件 → 仍是内容类（不提醒）
	plain := LeafInfoData{ScoreDeadline: 1773748800}
	plain.ContentInfo.ScoreEvaluation.Name = "雨课堂课件"
	if got := taskTypeFromInfo(plain); got != domain.TaskVideo {
		t.Errorf("不评分课件应保持内容类，实际 %v", got)
	}

	// 没有截止时间的计分课件 → 内容类（无可提醒的时间点）
	noDeadline := LeafInfoData{IsAssessed: true}
	noDeadline.ContentInfo.ScoreEvaluation.Name = "雨课堂课件"
	if got := taskTypeFromInfo(noDeadline); got != domain.TaskVideo {
		t.Errorf("无截止时间的课件应保持内容类，实际 %v", got)
	}

	// 真视频（标签"课堂"）即使 is_assessed 也不该变成作业。
	video := LeafInfoData{IsAssessed: true, ScoreDeadline: 1773748800}
	video.ContentInfo.ScoreEvaluation.Name = "课堂"
	if got := taskTypeFromInfo(video); got != domain.TaskVideo {
		t.Errorf("课堂录像不应被提升，实际 %v", got)
	}
}

// TestForumURLNeedsChapterID 锁定 forum 路由的 node_id 参数。
//
// 用户提供的真实链接：
//
//	/ai-workspace/lms-graph/25570088/forum/48057205?is_chapter=1&node_id=6338135
//	  └ 48057205 = 叶子 id，6338135 = 该叶子的 chapter_id
//
// 注意 node_id 参数**不是**叶子自己的 node_id 字段（同一条为 2308919）。
func TestForumURLNeedsChapterID(t *testing.T) {
	got := taskURLByKind(testBase, "25570088", "48057205", "6338135",
		"", "forum", routeChapter)
	want := testBase + "/ai-workspace/lms-graph/25570088/forum/48057205?is_chapter=1&node_id=6338135"
	if got != want {
		t.Errorf("forum URL = %q\nwant %q", got, want)
	}

	// 缺 chapter_id 时仍应能打开（只是内容区可能为空），不能退回首页。
	got = taskURLByKind(testBase, "25570088", "48057205", "",
		"", "forum", routeChapter)
	want = testBase + "/ai-workspace/lms-graph/25570088/forum/48057205?is_chapter=1"
	if got != want {
		t.Errorf("缺 chapter_id 的 forum URL = %q\nwant %q", got, want)
	}
}

// TestPaperRoutesUseCoursewareID 锁定老版页面路由。
//
// 实测：这两类页面只认学习日志给出的 courseware_id，
// 章节树的 id / leafinfo_id 都打不开。
func TestPaperRoutesUseCoursewareID(t *testing.T) {
	cases := []struct {
		name        string
		classroomID string
		courseware  string
		kind        routeKind
		want        string
	}{
		{
			name:        "考试走老版考试页",
			classroomID: "24984528",
			courseware:  "2051258",
			kind:        routeExamLegacy,
			want:        testBase + "/v2/web/exam/24984528/2051258",
		},
		{
			name:        "习题走老版成绩页",
			classroomID: "25570085",
			courseware:  "1745475",
			kind:        routeQuizSummary,
			want:        testBase + "/v2/web/quizSummary/25570085/1745475",
		},
	}
	for _, tc := range cases {
		got := taskURLByKind(testBase, tc.classroomID, "46656990", "6338135",
			tc.courseware, "quiz", tc.kind)
		if got != tc.want {
			t.Errorf("%s: URL = %q, want %q", tc.name, got, tc.want)
		}
	}

	// 缺 courseware_id 时必须退回章节路由，而不是拼出半截 URL。
	got := taskURLByKind(testBase, "24984528", "46656990", "6338135",
		"", "quiz", routeExamLegacy)
	want := testBase + "/ai-workspace/lms-graph/24984528/quiz/46656990?is_chapter=1"
	if got != want {
		t.Errorf("缺 courseware_id 应退回章节路由，得到 %q", got)
	}
}

// TestIsPaperLeaf 锁定"哪些 leaf_type 可能出现在学习日志里"。
//
// 只有试卷类才有 courseware_id 可供老版路由使用。
// 课件/视频/讨论即使标题撞上日志条目，也不能被改写成老版路由。
func TestIsPaperLeaf(t *testing.T) {
	want := map[int64]bool{5: true, 6: true, 9: true, 0: false, 3: false, 4: false, 7: false, 8: false}
	for lt, w := range want {
		if got := isPaperLeaf(lt); got != w {
			t.Errorf("isPaperLeaf(%d) = %v, want %v", lt, got, w)
		}
	}
}
