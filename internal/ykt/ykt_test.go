package ykt

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/domain"
)

func TestFlexInt(t *testing.T) {
	cases := []struct {
		raw  string
		want int64
	}{
		{`123`, 123},
		{`"123"`, 123},
		{`123.0`, 123},
		{`"123.0"`, 123},
		{`null`, 0},
		{`""`, 0},
		{`"abc"`, 0}, // 宽容：不报错
	}
	for _, c := range cases {
		var f FlexInt
		if err := json.Unmarshal([]byte(c.raw), &f); err != nil {
			t.Errorf("FlexInt(%s) 解析报错: %v", c.raw, err)
			continue
		}
		if f.Int64() != c.want {
			t.Errorf("FlexInt(%s) = %d, want %d", c.raw, f.Int64(), c.want)
		}
	}
}

func TestFlexTimeSecondsAndMillis(t *testing.T) {
	var sec, ms, empty FlexTime
	if err := json.Unmarshal([]byte(`1762750800`), &sec); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`1762750800000`), &ms); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`null`), &empty); err != nil {
		t.Fatal(err)
	}
	if sec.Unix() != 1762750800 {
		t.Errorf("秒级时间戳解析错误: %d", sec.Unix())
	}
	if ms.Unix() != 1762750800 {
		t.Errorf("毫秒级时间戳应归一到秒，实际 %d", ms.Unix())
	}
	if empty.Unix() != 0 {
		t.Errorf("null 应解析为 0，实际 %d", empty.Unix())
	}
}

// TestFlexTimeFloatMillis 锁定 bug #28。
//
// /leaf_info 返回的毫秒时间戳带小数（`"score_deadline": 1778428740000.0`）。
// 早期实现只用 ParseInt，解析失败后静默退化为 0，表现为"截止时间凭空消失"。
// 症状：计分课件因此无法被 is_assessed && score_deadline>0 识别，
// 停留在 TaskVideo，完全不参与截止提醒（33 个任务静默漏提醒）。
func TestFlexTimeFloatMillis(t *testing.T) {
	var f FlexTime
	if err := json.Unmarshal([]byte(`1778428740000.0`), &f); err != nil {
		t.Fatal(err)
	}
	if f.Unix() != 1778428740 {
		t.Errorf("浮点毫秒应解析为 1778428740，实际 %d", f.Unix())
	}

	// 秒级浮点也应支持。
	var g FlexTime
	if err := json.Unmarshal([]byte(`1762750800.0`), &g); err != nil {
		t.Fatal(err)
	}
	if g.Unix() != 1762750800 {
		t.Errorf("浮点秒应解析为 1762750800，实际 %d", g.Unix())
	}
}

func TestFlexTimeString(t *testing.T) {
	var f FlexTime
	if err := json.Unmarshal([]byte(`"2026-11-10 12:00:00"`), &f); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local).Unix()
	if f.Unix() != want {
		t.Errorf("字符串时间解析 = %d, want %d", f.Unix(), want)
	}
}

func TestFlexTimeUnparseable(t *testing.T) {
	var f FlexTime
	if err := json.Unmarshal([]byte(`"not-a-time"`), &f); err != nil {
		t.Errorf("无法解析的时间不应报错（宽容策略），实际: %v", err)
	}
	if f.Unix() != 0 {
		t.Errorf("无法解析时应为 0，实际 %d", f.Unix())
	}
}

func TestMapLeafType(t *testing.T) {
	// 按 2026-11 真实账号 464 个叶子校准，依据是服务端自己在
	// /leaf_info 的 content_info.score_evaluation.name 里给出的标签
	// （161 个样本逐条比对）：
	//
	//	0 视频  / 3 图文 / 7 雨课堂课件 / 8 课堂 → TaskVideo
	//	4 讨论                                    → TaskDiscuss
	//	5 作业/考试、6 作业、9 考试               → TaskHomework
	//
	// 3 和 7 归为 TaskVideo 不是"显示成视频"，而是因为它们与视频共用
	// 同一个内容阅读器：只有 /video/{id} 路由能打开，走 quiz 报
	// "没有权限"、走 exercise 报"页面不存在"、走 node 报"知识点不存在"。
	cases := map[int64]domain.TaskType{
		0:  domain.TaskVideo,
		3:  domain.TaskVideo,
		4:  domain.TaskDiscuss,
		5:  domain.TaskHomework,
		6:  domain.TaskHomework,
		7:  domain.TaskHomework,
		8:  domain.TaskVideo,
		9:  domain.TaskHomework,
		99: domain.TaskOther, // 未知类型退化
	}
	for in, want := range cases {
		if got := mapLeafType(in); got != want {
			t.Errorf("mapLeafType(%d) = %v, want %v", in, got, want)
		}
	}
}

// TestClassifyPrefersTitle 验证名称关键词与 leaf_type 的优先级。
//
// 真实数据里同一 leaf_type=6 既可能是"作业-第1章"也可能是"讨论-导论"，
// 只看数字必然误判，因此标题关键词优先。
//
// 但 leaf_type 能确定语义时（4=讨论、3/7/8=内容阅读器）不让标题改写：
//   - 「小组作业——高斯分布的应用」标题含"作业"，实际是讨论（forum 路由）
//   - 「课程前后复习&预习：指针与引用」标题含"预习"，实际是课件（video 路由）
//
// 这两条都来自真机验证：按标题判成作业会生成 quiz 链接，
// 而 quiz 路由对它们报"没有权限查看该页面"。
func TestClassifyPrefersTitle(t *testing.T) {
	cases := []struct {
		title    string
		leafType int64
		want     domain.TaskType
	}{
		{"作业-第1章", 6, domain.TaskHomework},
		{"讨论-导论", 6, domain.TaskDiscuss},
		{"期中2026", 7, domain.TaskExam},
		{"随堂测验2.4", 9, domain.TaskHomework},
		{"第一节：提升信息素养", 8, domain.TaskVideo},
		{"课程简介", 0, domain.TaskVideo},
		// leaf_type=4 是讨论/小组作业，标题里的"作业"不能改写类型。
		{"小组作业——高斯分布的应用", 4, domain.TaskDiscuss},
		{"课后思考：乘法公式的应用——空战问题", 4, domain.TaskDiscuss},
		{"普通节点", 5, domain.TaskHomework}, // 无关键词，回退 leaf_type
		{"未知节点", 99, domain.TaskOther},   // 都无，退化
	}
	for _, c := range cases {
		if got := classify(c.title, c.leafType); got != c.want {
			t.Errorf("classify(%q, %d) = %v, want %v", c.title, c.leafType, got, c.want)
		}
	}
}

func TestCollectLeavesNested(t *testing.T) {
	chapters := []ChapterNode{
		{
			Name: "第一章",
			Children: []ChapterNode{
				{Name: "1.1", LeafID: "L1", LeafType: 5, ScoreDeadline: 1762750800},
				{
					Name: "1.2 子节点",
					Children: []ChapterNode{
						{Name: "作业1", LeafID: "L2", LeafType: 5, EndTime: 1762837200},
					},
				},
			},
		},
	}

	leaves := collectLeaves(chapters, "高数", "C1")
	if len(leaves) != 2 {
		t.Fatalf("应收集到 2 个叶子，实际 %d", len(leaves))
	}
	if leaves[0].leafID != "L1" || leaves[0].deadline != 1762750800 {
		t.Errorf("第一个叶子解析错误: %+v", leaves[0])
	}
	// score_deadline 缺失时应回落到 end_time
	if leaves[1].leafID != "L2" || leaves[1].deadline != 1762837200 {
		t.Errorf("第二个叶子应回落到 end_time: %+v", leaves[1])
	}
}

func TestCollectLeavesSkipsContainers(t *testing.T) {
	// 没有 leaf_id / 没有类型 / 没有时间 → 纯章节容器，不应被当作任务
	chapters := []ChapterNode{{Name: "第一章", ID: "0"}}
	if got := collectLeaves(chapters, "课程", "C1"); len(got) != 0 {
		t.Errorf("纯章节容器不应被当作任务，实际 %d", len(got))
	}
}

func TestTaskIDStable(t *testing.T) {
	a := taskID("C1", "L1")
	b := taskID("C1", "L1")
	if a != b {
		t.Error("同一课堂同一叶子应生成相同 ID")
	}
	if a == taskID("C1", "L2") {
		t.Error("不同叶子应生成不同 ID")
	}
}

func TestApplyLeafInfoOverrides(t *testing.T) {
	base := domain.Task{
		ID:       "ykt:C1:L1",
		Type:     domain.TaskHomework,
		Title:    "旧标题",
		Deadline: time.Unix(1762750800, 0),
		Status:   domain.StatusUnknown,
	}
	info := LeafInfoData{
		Title:         "新标题",
		ScoreDeadline: 1762837200,
	}

	got := applyLeafInfo(base, info)
	if got.Title != "新标题" {
		t.Errorf("标题应被详情覆盖，实际 %q", got.Title)
	}
	if got.Deadline.Unix() != 1762837200 {
		t.Errorf("截止时间应被详情覆盖，实际 %d", got.Deadline.Unix())
	}
	// 详情接口不提供完成状态（真实响应无 is_finish/status），
	// 因此这里必须保持 unknown —— 完成状态改由学习日志提供。
	if got.Status != domain.StatusUnknown {
		t.Errorf("详情不应擅自决定完成状态，实际 %v", got.Status)
	}
}

// TestApplyLeafInfoIgnoresClassEndTime 锁定 bug #26：
// class_end_time 是整门课的结束时间，绝不能当任务截止时间兜底。
//
// 真实数据：《第一章习题》score_deadline=2026-10-15、class_end_time=2027-01-31。
// 早期实现在 score_deadline 缺失时用 class_end_time 兜底，会让已截止的
// 作业显示成"还有 84 天"。
func TestApplyLeafInfoIgnoresClassEndTime(t *testing.T) {
	base := domain.Task{
		ID:     "ykt:C1:L1",
		Type:   domain.TaskHomework,
		Title:  "《第一章习题》",
		Status: domain.StatusUnknown,
	}
	info := LeafInfoData{
		Title: "《第一章习题》",
		// 只有课程结束时间，没有任务截止时间。
		ClassEndTime: 1801411199, // 2027-01-31
	}

	got := applyLeafInfo(base, info)
	if !got.Deadline.IsZero() {
		t.Errorf("class_end_time 不应被当成截止时间，实际 %v", got.Deadline)
	}
}

func TestDedupeTasks(t *testing.T) {
	tasks := []domain.Task{
		{ID: "a", Title: "短", Deadline: time.Unix(1762750800, 0)},
		{ID: "a", Title: "更长的标题", Deadline: time.Unix(1762750800, 0)},
		{ID: "b", Title: "另一个"},
	}
	got := dedupeTasks(tasks)
	if len(got) != 2 {
		t.Fatalf("去重后应有 2 条，实际 %d", len(got))
	}
	if got[0].Title != "更长的标题" {
		t.Errorf("应保留信息更完整的一条，实际 %q", got[0].Title)
	}
}

func TestLooksLikeHTML(t *testing.T) {
	cases := map[string]bool{
		`<!DOCTYPE html><html></html>`: true,
		`<html><body>x</body></html>`:  true,
		`{"success":true}`:             false,
	}
	for in, want := range cases {
		if got := looksLikeHTML([]byte(in)); got != want {
			t.Errorf("looksLikeHTML(%q) = %v, want %v", in, got, want)
		}
	}
}
