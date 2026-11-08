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
// 课件可以作为阅读材料展示并继续走 video 路由。
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

// TestAssessedCoursewareRemainsReadingMaterial 锁定计分/考核字段不产生伪提交任务。
//
// 真机样本中，计分课件打开的是 video 阅读器，没有提交或答题入口；
// 过截止后学习行为仍会记录，但完成度不更新。结论只覆盖已核验样本。
func TestAssessedCoursewareRemainsReadingMaterial(t *testing.T) {
	assessed := LeafInfoData{
		ScoreDeadline: 1773748800,
		IsAssessed:    true,
	}
	assessed.ContentInfo.ScoreEvaluation.Name = "雨课堂课件"
	if got := taskTypeFromInfo(assessed); got != domain.TaskVideo {
		t.Errorf("计分课件应保持阅读材料，实际 %v", got)
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

	// 真正的作业与考试仍按服务端标签参与提醒。
	homework := LeafInfoData{IsAssessed: true, ScoreDeadline: 1773748800}
	homework.ContentInfo.ScoreEvaluation.Name = "作业"
	if got := taskTypeFromInfo(homework); got != domain.TaskHomework {
		t.Errorf("作业标签应保持 homework，实际 %v", got)
	}
	exam := LeafInfoData{IsAssessed: true, ScoreDeadline: 1773748800}
	exam.ContentInfo.ScoreEvaluation.Name = "考试"
	if got := taskTypeFromInfo(exam); got != domain.TaskExam {
		t.Errorf("考试标签应保持 exam，实际 %v", got)
	}
}

// TestForumURLNeedsChapterID 锁定 forum 路由的 node_id 参数。
//
// 用户提供的脱敏链接示例：
//
//	/ai-workspace/lms-graph/80000008/forum/80000022?is_chapter=1&node_id=7000008
//	  └ 80000022 = 叶子 id，7000008 = 该叶子的 chapter_id
//
// 注意 node_id 参数**不是**叶子自己的 node_id 字段（同一条为 7000004）。
func TestForumURLNeedsChapterID(t *testing.T) {
	got := taskURLByKind(testBase, "80000008", "80000022", "7000008",
		"", "forum", routeChapter)
	want := testBase + "/ai-workspace/lms-graph/80000008/forum/80000022?is_chapter=1&node_id=7000008"
	if got != want {
		t.Errorf("forum URL = %q\nwant %q", got, want)
	}

	// 缺 chapter_id 时仍应能打开（只是内容区可能为空），不能退回首页。
	got = taskURLByKind(testBase, "80000008", "80000022", "",
		"", "forum", routeChapter)
	want = testBase + "/ai-workspace/lms-graph/80000008/forum/80000022?is_chapter=1"
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
			classroomID: "80000006",
			courseware:  "7000003",
			kind:        routeExamLegacy,
			want:        testBase + "/v2/web/exam/80000006/7000003",
		},
		{
			name:        "习题走老版成绩页",
			classroomID: "80000007",
			courseware:  "7000001",
			kind:        routeQuizSummary,
			want:        testBase + "/v2/web/quizSummary/80000007/7000001",
		},
	}
	for _, tc := range cases {
		got := taskURLByKind(testBase, tc.classroomID, "80000020", "7000008",
			tc.courseware, "quiz", tc.kind)
		if got != tc.want {
			t.Errorf("%s: URL = %q, want %q", tc.name, got, tc.want)
		}
	}

	// 缺 courseware_id 时必须退回章节路由，而不是拼出半截 URL。
	got := taskURLByKind(testBase, "80000006", "80000020", "7000008",
		"", "quiz", routeExamLegacy)
	want := testBase + "/ai-workspace/lms-graph/80000006/quiz/80000020?is_chapter=1"
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
