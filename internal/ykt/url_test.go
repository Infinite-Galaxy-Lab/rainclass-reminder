package ykt

import (
	"strings"
	"testing"

	"github.com/2963609262/ykt-reminder/internal/domain"
)

// 本文件锁定"任务 → 网页端直达链接"的生成规则。
//
// 路由模板来自对真实网页端的确认（2026-11）。证据是用户提供的、
// 可正常打开的页面：
//
//	/ai-workspace/lms-graph/25570088/quiz/48057249?is_chapter=1
//
// 其中 25570088 是 classroom_id，《第三章习题》的 node_id 是 48057249，
// 而它的 leafinfo_id 是 48059470 —— 证明路由要的是 **node id**。
// 任何一条规则被改动，这里应当先失败，避免退回"全部跳首页"或
// "打开提示内容已被删除"的老问题。全部为纯函数测试，不依赖网络。

func TestWebTaskURL(t *testing.T) {
	const base = "https://changjiang.yuketang.cn"

	// 用户提供的真实样例，用于断言"和真实 URL 完全一致"。
	const (
		realClassroom = "25570088"
		realNodeID    = "48057249"
		realURL       = base + "/ai-workspace/lms-graph/25570088/quiz/48057249?is_chapter=1"
	)

	cases := []struct {
		name        string
		classroomID string
		leafID      string
		typ         domain.TaskType
		examID      string
		chapterID   string
		want        string
	}{
		{
			name:        "作业走 ai-workspace quiz 路由（与真实页面一致）",
			classroomID: realClassroom,
			leafID:      realNodeID,
			typ:         domain.TaskHomework,
			want:        realURL,
		},
		{
			name:        "考试有 exam_id 时走老版 exam 页（不是 ai-workspace）",
			classroomID: realClassroom,
			leafID:      realNodeID,
			typ:         domain.TaskExam,
			examID:      "2051258",
			want:        base + "/v2/web/exam/25570088/2051258",
		},
		{
			name:        "考试缺 exam_id 时退回章节路由（至少能看上下文）",
			classroomID: realClassroom,
			leafID:      realNodeID,
			typ:         domain.TaskExam,
			want:        realURL,
		},
		{
			// 用户提供的真实链接（2026-11-08 确认可打开）：
			// /ai-workspace/lms-graph/25570088/forum/48057205?is_chapter=1&node_id=6338135
			// 其中 48057205 是叶子 id、6338135 是该叶子的 chapter_id。
			name:        "讨论带 chapter_id 时补齐 node_id 参数（真实链接逐字一致）",
			classroomID: realClassroom,
			leafID:      "48057205",
			typ:         domain.TaskDiscuss,
			chapterID:   "6338135",
			want:        base + "/ai-workspace/lms-graph/25570088/forum/48057205?is_chapter=1&node_id=6338135",
		},
		{
			name:        "讨论缺 chapter_id 时仍能打开（只是内容区可能为空）",
			classroomID: realClassroom,
			leafID:      realNodeID,
			typ:         domain.TaskDiscuss,
			want:        base + "/ai-workspace/lms-graph/25570088/forum/48057249?is_chapter=1",
		},
		{
			name:        "视频走 video 路由",
			classroomID: realClassroom,
			leafID:      realNodeID,
			typ:         domain.TaskVideo,
			want:        base + "/ai-workspace/lms-graph/25570088/video/48057249?is_chapter=1",
		},
		{
			name:        "未知类型落通用 node 页（仍有内容可看）",
			classroomID: realClassroom,
			leafID:      realNodeID,
			typ:         domain.TaskClass,
			want:        base + "/ai-workspace/lms-graph/25570088/node/48057249?is_chapter=1",
		},
		{
			name:        "缺 classroom 退回首页",
			classroomID: "",
			leafID:      realNodeID,
			typ:         domain.TaskHomework,
			want:        base + "/v2/web/index",
		},
		{
			name:        "缺 leaf 且非考试退回首页",
			classroomID: realClassroom,
			leafID:      "",
			typ:         domain.TaskDiscuss,
			want:        base + "/v2/web/index",
		},
		{
			name:        "缺 leaf 但考试有 exam_id 时仍能拼出链接",
			classroomID: realClassroom,
			leafID:      "",
			typ:         domain.TaskExam,
			examID:      "90001",
			want:        base + "/v2/web/exam/25570088/90001",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := webTaskURL(base, tc.classroomID, tc.leafID, tc.typ, tc.examID, tc.chapterID)
			if got != tc.want {
				t.Errorf("webTaskURL() = %q, want %q", got, tc.want)
			}
			if !strings.HasPrefix(got, base) {
				t.Errorf("链接应保留站点根地址: %q", got)
			}
		})
	}
}

// TestTaskURLUsesNodeIDNotLeafInfoID 是这次 bug 的核心回归测试。
//
// 真实数据：《第三章习题》node_id=48057249、leafinfo_id=48059470。
// 用 leafinfo_id 会打开「此内容已被老师删除」，必须用 node id。
func TestTaskURLUsesNodeIDNotLeafInfoID(t *testing.T) {
	const base = "https://changjiang.yuketang.cn"

	c := candidateLeaf{
		course:     "概率论与数理统计",
		classroom:  "25570088",
		leafID:     "48057249", // node id
		leafInfoID: "48059470", // leafinfo_id，绝不能出现在 URL 里
		title:      "《第三章习题》",
		leafType:   5,
	}

	task := toTask(c, base)
	want := base + "/ai-workspace/lms-graph/25570088/quiz/48057249?is_chapter=1"
	if task.URL != want {
		t.Errorf("URL = %q\nwant %q", task.URL, want)
	}
	if strings.Contains(task.URL, "48059470") {
		t.Errorf("URL 里不能出现 leafinfo_id（会打开“内容已被老师删除”）：%q", task.URL)
	}
	// 任务 ID 仍应基于 node id，保证跨轮次稳定。
	if task.ID != "ykt:25570088:48057249" {
		t.Errorf("任务 ID 应基于 node id，实际 %q", task.ID)
	}
}

// TestToTaskURLByType 验证不同 leaf_type 拿到正确路由。
//
// 全部用例的 leaf_type 与期望路由都来自真机验证（2026-11-08）：
// 用浏览器逐条打开，以"页面是否真的渲染出内容"为准，
// 而不是以"URL 看起来对不对"为准。
func TestToTaskURLByType(t *testing.T) {
	const base = "https://changjiang.yuketang.cn"

	cases := []struct {
		name string
		leaf candidateLeaf
		want string
	}{
		{
			// leaf_type=4 是讨论，标题里的"作业"不能改写它。
			// 用户提供的真实链接：/forum/48057205?is_chapter=1&node_id=6338135
			name: "讨论叶子带 chapter_id（node_id 参数来自 chapter_id）",
			leaf: candidateLeaf{
				classroom: "C1", leafID: "L4", leafInfoID: "X4",
				title: "小组作业——高斯分布的应用", leafType: 4, chapterID: "CH1",
			},
			want: base + "/ai-workspace/lms-graph/C1/forum/L4?is_chapter=1&node_id=CH1",
		},
		{
			name: "讨论叶子缺 chapter_id 时仍能打开",
			leaf: candidateLeaf{classroom: "C1", leafID: "L4", leafInfoID: "X4", title: "第一章讨论", leafType: 4},
			want: base + "/ai-workspace/lms-graph/C1/forum/L4?is_chapter=1",
		},
		{
			name: "视频叶子（leaf_type=8）走 video",
			leaf: candidateLeaf{classroom: "C1", leafID: "L1", leafInfoID: "X1", title: "1.1 课程视频", leafType: 8},
			want: base + "/ai-workspace/lms-graph/C1/video/L1?is_chapter=1",
		},
		{
			// leaf_type=7 是"雨课堂课件"。实测它有时叫「作业1单元」
			// 有时叫「期中2026」，但服务端标的是"雨课堂课件"，
			// 只有 video 路由能打开（quiz 报"没有权限"）。
			name: "课件叶子（leaf_type=7，标题含作业）仍走 video",
			leaf: candidateLeaf{classroom: "C1", leafID: "L7", leafInfoID: "X7", title: "作业1单元", leafType: 7},
			want: base + "/ai-workspace/lms-graph/C1/video/L7?is_chapter=1",
		},
		{
			name: "图文叶子（leaf_type=3）走 video",
			leaf: candidateLeaf{classroom: "C1", leafID: "L3", leafInfoID: "X3", title: "随机试验与随机事件--课件", leafType: 3},
			want: base + "/ai-workspace/lms-graph/C1/video/L3?is_chapter=1",
		},
		{
			name: "习题叶子（leaf_type=5）走 quiz",
			leaf: candidateLeaf{classroom: "C1", leafID: "L9", leafInfoID: "X9", title: "第三章习题", leafType: 5},
			want: base + "/ai-workspace/lms-graph/C1/quiz/L9?is_chapter=1",
		},
		{
			name: "随堂测验叶子（leaf_type=9）走 quiz",
			leaf: candidateLeaf{classroom: "C1", leafID: "L8", leafInfoID: "X8", title: "随堂测验2.4", leafType: 9},
			want: base + "/ai-workspace/lms-graph/C1/quiz/L8?is_chapter=1",
		},
		{
			// 章节树带 exam_id 的试卷叶子是"唯一能确定考试实体"的情况，
			// 直接走老版考试页。实测该字段全站为空，属于兜底分支。
			name: "试卷叶子带 exam_id 时走老版考试页",
			leaf: candidateLeaf{
				classroom: "C1", leafID: "L5", leafInfoID: "X5",
				title: "期中考试", leafType: 5, examID: "90001",
			},
			want: base + "/v2/web/exam/C1/90001",
		},
		{
			name: "试卷叶子缺 exam_id 时走章节 quiz 路由",
			leaf: candidateLeaf{
				classroom: "C1", leafID: "L5", leafInfoID: "X5",
				title: "期中考试", leafType: 5,
			},
			want: base + "/ai-workspace/lms-graph/C1/quiz/L5?is_chapter=1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := toTask(tc.leaf, base)
			if task.URL != tc.want {
				t.Errorf("URL = %q, want %q", task.URL, tc.want)
			}
		})
	}
}

// TestExamToTaskURL 验证考试封面路径生成的链接带 exam_id。
//
// 考试必须落到老版 /v2/web/exam/{cid}/{exam_id}，不能用 ai-workspace 的
// quiz 路由——后者只能定位到章节位置，打不开试卷本身。
// 真实链接：/v2/web/exam/24984528/2051258
func TestExamToTaskURL(t *testing.T) {
	const base = "https://changjiang.yuketang.cn"

	d := ExamCoverData{}
	d.ExamID = FlexString("90001")
	task := examToTask("高等数学", "882136", d, base)

	want := base + "/v2/web/exam/882136/90001"
	if task.URL != want {
		t.Errorf("URL = %q, want %q", task.URL, want)
	}
}
