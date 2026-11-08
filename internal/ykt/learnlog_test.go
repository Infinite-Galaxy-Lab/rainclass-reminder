package ykt

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/domain"
)

// TestCoursewareIndexFromLearnLogs 锁定"用学习日志补全试卷 ID"的行为。
//
// 脱敏样例（2026-11，用户在浏览器里确认可正常打开的页面）：
//
//	模拟题1       learning log: type=5 courseware_id=7000003
//	              → /v2/web/exam/80000006/7000003
//	T1.1.4 PN结   learning log: type=4 courseware_id=7000001
//	              → /v2/web/quizSummary/80000007/7000001
//
// 章节树里根本没有这两个 ID：exam.exam_id 全站为空、
// leaf_info 详情对这两类返回 404。
func TestCoursewareIndexFromLearnLogs(t *testing.T) {
	const (
		examClassroom = "80000006"
		examNodeID    = "80000020"
		examID        = "7000003"
		examTitle     = "模拟题1:2026年大学生“AI+信息素养”大赛"

		quizClassroom = "80000007"
		quizNodeID    = "90000010"
		quizID        = "7000001"
		quizTitle     = "T1.1.4 PN结的形成及特性"
		decimalNodeID = "90000012"
		decimalID     = "7000004"
		decimalTitle  = "T1.2 小数成绩"
		missingNodeID = "90000013"
		missingID     = "7000005"
		missingTitle  = "T1.3 缺失成绩"
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/api/web/courses/list", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":0,"data":{"list":[
		  {"classroom_id":"` + examClassroom + `","name":"备赛培训"},
		  {"classroom_id":"` + quizClassroom + `","name":"模拟电子技术"}
		]}}`))
	})

	mux.HandleFunc("/mooc-api/v1/lms/learn/course/chapter", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("cid") {
		case examClassroom:
			// 注意：exam 对象里**没有** exam_id（真实情况就是这样）。
			_, _ = w.Write([]byte(`{"success":true,"errcode":0,"data":{"course_chapter":[{
			  "id": 90000001, "name": "备赛",
			  "section_leaf_list": [{
			    "id": ` + examNodeID + `, "leafinfo_id": 90000007, "leaf_type": 5,
			    "name": "` + examTitle + `",
			    "score_deadline": 1798678800000,
			    "exam": {"online_proctor": 0, "is_offline": false}
			  }]
			}]}}`))
		case quizClassroom:
			_, _ = w.Write([]byte(`{"success":true,"errcode":0,"data":{"course_chapter":[{
			  "id": 90000002, "name": "第一章",
			  "section_leaf_list": [{
			    "id": ` + quizNodeID + `, "leafinfo_id": 90000011, "leaf_type": 9,
			    "name": "` + quizTitle + `",
			    "score_deadline": 1801411199000
			  }, {
			    "id": ` + decimalNodeID + `, "leafinfo_id": 90000014, "leaf_type": 9,
			    "name": "` + decimalTitle + `",
			    "score_deadline": 1801411199000
			  }, {
			    "id": ` + missingNodeID + `, "leafinfo_id": 90000015, "leaf_type": 9,
			    "name": "` + missingTitle + `",
			    "score_deadline": 1801411199000
			  }]
			}]}}`))
		default:
			http.NotFound(w, r)
		}
	})

	// 学习日志：唯一能给出真实 courseware_id 的地方。
	mux.HandleFunc("/v2/api/web/logs/learn/", func(w http.ResponseWriter, r *http.Request) {
		// 校验必须带 actype=-1（actype=100 实测返回空）。
		if r.URL.Query().Get("actype") != "-1" {
			_, _ = w.Write([]byte(`{"errcode":0,"data":{"activities":[],"has_more":false}}`))
			return
		}
		switch {
		case strings.Contains(r.URL.Path, examClassroom):
			_, _ = w.Write([]byte(`{"errcode":0,"data":{"activities":[
			  {"type":5,"id":90000004,"courseware_id":"` + examID + `","title":"` + examTitle + `","total_score":100,"problem_count":50,"status":1}
			],"has_more":false}}`))
		case strings.Contains(r.URL.Path, quizClassroom):
			_, _ = w.Write([]byte(`{"errcode":0,"data":{"activities":[
			  {"type":4,"id":90000005,"courseware_id":"` + quizID + `","title":"` + quizTitle + `","score":0,"total_score":5,"problem_count":4,"status":3},
			  {"type":4,"id":90000007,"courseware_id":"` + decimalID + `","title":"` + decimalTitle + `","score":2.75,"total_score":4.5,"problem_count":4,"status":1},
			  {"type":4,"id":90000008,"courseware_id":"` + missingID + `","title":"` + missingTitle + `","problem_count":4,"status":0},
			  {"type":14,"id":90000006,"courseware_id":"90000003","title":"` + quizTitle + `","status":3}
			],"has_more":false}}`))
		default:
			_, _ = w.Write([]byte(`{"errcode":0,"data":{"activities":[],"has_more":false}}`))
		}
	})

	mux.HandleFunc("/mooc-api/v1/lms/learn/leaf_info/", func(w http.ResponseWriter, r *http.Request) {
		// 真实情况：考试/习题的详情返回 404。
		_, _ = w.Write([]byte(`{"success":false,"error_code":404000,"msg":"Objects does not exist.","data":{}}`))
	})

	srv := newFakeServer(t, mux)
	c, err := New(Options{
		BaseURL:     srv.URL,
		Identity:    2,
		Concurrency: 2,
		ChapterTTL:  time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}

	tasks, stats, err := c.FetchAll(context.Background(), 30)
	if err != nil {
		t.Fatalf("抓取失败: %v", err)
	}
	if stats.LinksFromLogs < 2 {
		t.Errorf("应有至少 2 个链接来自学习日志，实际 %d", stats.LinksFromLogs)
	}

	byID := make(map[string]string, len(tasks))
	byTaskID := make(map[string]domain.Task, len(tasks))
	for _, tk := range tasks {
		byID[tk.ID] = tk.URL
		byTaskID[tk.ID] = tk
	}

	// 考试：必须走老版 exam 路由 + exam_id（courseware_id）。
	wantExam := srv.URL + "/v2/web/exam/" + examClassroom + "/" + examID
	if got := byID["ykt:"+examClassroom+":"+examNodeID]; got != wantExam {
		t.Errorf("考试链接 = %q\nwant %q", got, wantExam)
	}

	// 习题：必须走 quizSummary + quiz_id（courseware_id）。
	wantQuiz := srv.URL + "/v2/web/quizSummary/" + quizClassroom + "/" + quizID
	if got := byID["ykt:"+quizClassroom+":"+quizNodeID]; got != wantQuiz {
		t.Errorf("习题链接 = %q\nwant %q", got, wantQuiz)
	}

	zero := byTaskID["ykt:"+quizClassroom+":"+quizNodeID]
	if zero.Score == nil || *zero.Score != 0 || zero.TotalScore == nil || *zero.TotalScore != 5 {
		t.Errorf("零分任务分数丢失: score=%v total=%v", zero.Score, zero.TotalScore)
	}
	if zero.Status != domain.StatusDone {
		t.Errorf("零分但 status=3 应保持已完成，实际 %v", zero.Status)
	}

	decimal := byTaskID["ykt:"+quizClassroom+":"+decimalNodeID]
	if decimal.Score == nil || *decimal.Score != 2.75 || decimal.TotalScore == nil || *decimal.TotalScore != 4.5 {
		t.Errorf("小数分数未保留: score=%v total=%v", decimal.Score, decimal.TotalScore)
	}
	if decimal.Status != domain.StatusPending {
		t.Errorf("status=1 应保持未完成，实际 %v", decimal.Status)
	}

	missing := byTaskID["ykt:"+quizClassroom+":"+missingNodeID]
	if missing.Score != nil || missing.TotalScore != nil {
		t.Errorf("缺失分数应保持 nil: score=%v total=%v", missing.Score, missing.TotalScore)
	}
	if missing.Status != domain.StatusPending {
		t.Errorf("status=0 应保持未完成，实际 %v", missing.Status)
	}

	// 绝不能再出现 node id 或 leafinfo_id 拼的链接。
	for id, u := range byID {
		if strings.Contains(u, examNodeID) || strings.Contains(u, quizNodeID) {
			t.Errorf("%s 的链接含章节节点 id（应使用 courseware_id）: %q", id, u)
		}
		if strings.Contains(u, "90000007") || strings.Contains(u, "90000011") {
			t.Errorf("%s 的链接含 leafinfo_id: %q", id, u)
		}
	}
}

// TestLearnLogTypeFilters 验证只有 type=4/5 被当成试卷。
//
// type=14 是视频，它的 courseware_id 不是试卷 ID；误用会打开错误页面。
func TestLearnLogTypeFilters(t *testing.T) {
	idx := &coursewareIndex{byTitle: map[string]coursewareRef{}}
	logs := []LearnLogActivity{
		{Type: FlexInt(LearnLogExam), CoursewareID: FlexString("111"), Title: FlexString("考试A")},
		{Type: FlexInt(LearnLogQuiz), CoursewareID: FlexString("222"), Title: FlexString("习题B")},
		{Type: FlexInt(LearnLogVideo), CoursewareID: FlexString("333"), Title: FlexString("视频C")},
		{Type: FlexInt(LearnLogNote), CoursewareID: FlexString("444"), Title: FlexString("公告D")},
		{Type: FlexInt(LearnLogQuiz), CoursewareID: FlexString(""), Title: FlexString("空ID")},
	}
	for _, a := range logs {
		id := strings.TrimSpace(a.CoursewareID.String())
		if id == "" {
			continue
		}
		kind := a.Type.Int64()
		if kind != LearnLogQuiz && kind != LearnLogExam {
			continue
		}
		idx.byTitle[normalizeTitle(a.Title.String())] = coursewareRef{coursewareID: id, kind: kind}
	}

	if _, ok := idx.lookup("视频C"); ok {
		t.Error("视频类不应进入索引")
	}
	if _, ok := idx.lookup("公告D"); ok {
		t.Error("公告类不应进入索引")
	}
	if _, ok := idx.lookup("空ID"); ok {
		t.Error("空 courseware_id 不应进入索引")
	}
	if ref, ok := idx.lookup("考试A"); !ok || ref.coursewareID != "111" {
		t.Errorf("考试A 未正确索引: %+v", ref)
	}
	if ref, ok := idx.lookup("习题B"); !ok || ref.kind != LearnLogQuiz {
		t.Errorf("习题B 未正确索引: %+v", ref)
	}
}

// TestCoursewareWebURLRouting 验证 type → 路由的映射。
func TestCoursewareWebURLRouting(t *testing.T) {
	const base = "https://changjiang.yuketang.cn"
	const cid = "80000008"

	cases := []struct {
		name string
		ref  coursewareRef
		want string
	}{
		{
			name: "考试走 exam 路由",
			ref:  coursewareRef{coursewareID: "7000003", kind: LearnLogExam},
			want: base + "/v2/web/exam/80000008/7000003",
		},
		{
			name: "习题走 quizSummary 路由",
			ref:  coursewareRef{coursewareID: "7000001", kind: LearnLogQuiz},
			want: base + "/v2/web/quizSummary/80000008/7000001",
		},
		{
			name: "未知类型不生成链接",
			ref:  coursewareRef{coursewareID: "999", kind: 14},
			want: "",
		},
		{
			name: "空 ID 不生成链接",
			ref:  coursewareRef{coursewareID: "", kind: LearnLogExam},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ref.webURL(base, cid); got != tc.want {
				t.Errorf("webURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNormalizeTitleMatching 验证标题匹配的严格程度。
//
// 实测章节树与学习日志的标题连全角引号都一致，所以只做空白归一化。
// 过度归一化会把"模拟题1"和"模拟题 1"之外的不相干任务也匹配上。
func TestNormalizeTitleMatching(t *testing.T) {
	cases := []struct {
		a, b  string
		equal bool
	}{
		{"模拟题1:2026年大学生“AI+信息素养”大赛", "模拟题1:2026年大学生“AI+信息素养”大赛", true},
		{" T1.1.4  PN结的形成及特性 ", "T1.1.4 PN结的形成及特性", true},
		{"第一章习题", "第二章习题", false},
		{"模拟题1", "模拟题1（2）", false},
	}
	for _, tc := range cases {
		got := normalizeTitle(tc.a) == normalizeTitle(tc.b)
		if got != tc.equal {
			t.Errorf("normalizeTitle(%q) == normalizeTitle(%q) 应为 %v", tc.a, tc.b, tc.equal)
		}
	}
}

// TestLearnLogsPagination 验证 has_more 分页逻辑。
func TestLearnLogsPagination(t *testing.T) {
	const cid = "C1"
	var pagesHit int

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/api/web/logs/learn/", func(w http.ResponseWriter, r *http.Request) {
		pagesHit++
		page := r.URL.Query().Get("page")
		if page == "0" {
			_, _ = w.Write([]byte(`{"errcode":0,"data":{"activities":[
			  {"type":4,"courseware_id":"11","title":"A"}
			],"has_more":true}}`))
			return
		}
		_, _ = w.Write([]byte(`{"errcode":0,"data":{"activities":[
		  {"type":5,"courseware_id":"22","title":"B"}
		],"has_more":false}}`))
	})

	srv := newFakeServer(t, mux)
	c, err := New(Options{BaseURL: srv.URL, Identity: 2})
	if err != nil {
		t.Fatal(err)
	}

	logs, err := c.LearnLogs(context.Background(), cid)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Errorf("应翻页拿到 2 条，实际 %d", len(logs))
	}
	if pagesHit != 2 {
		t.Errorf("应请求 2 页，实际 %d 页", pagesHit)
	}

	// 第二次调用应命中缓存，不再发请求。
	before := pagesHit
	if _, err := c.LearnLogs(context.Background(), cid); err != nil {
		t.Fatal(err)
	}
	if pagesHit != before {
		t.Errorf("第二次调用应命中缓存，实际又发了 %d 个请求", pagesHit-before)
	}
}
