package ykt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/domain"
)

// fakeServer 模拟雨课堂的三类只读接口，返回真实结构（脱敏后）的 JSON。
//
// 用途：在没有真实账号的情况下验证"HTTP → DTO → domain.Task"的完整链路，
// 并锁定字段名，接口变更时这个测试会先失败。
func newFakeServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchAllEndToEnd(t *testing.T) {
	mux := http.NewServeMux()

	// 课程列表
	mux.HandleFunc("/v2/api/web/courses/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "success": true, "errcode": 0,
		  "data": {"courses": [
		    {"classroom_id": "C1", "name": "高等数学"},
		    {"classroom_id": "C2", "name": "线性代数"}
		  ]}
		}`))
	})

	// 章节树（含嵌套章节与叶子）
	mux.HandleFunc("/mooc-api/v1/lms/learn/course/chapter", func(w http.ResponseWriter, r *http.Request) {
		cid := r.URL.Query().Get("cid")
		w.Header().Set("Content-Type", "application/json")
		switch cid {
		case "C1":
			_, _ = w.Write([]byte(`{
			  "success": true, "errcode": 0,
			  "data": {"chapters": [{
			    "name": "第一章 极限",
			    "children": [
			      {"name": "1.1 数列极限", "leaf_id": "L1", "leaf_type": 0, "end_time": 1762750800},
			      {"name": "第一章作业", "leaf_id": "L2", "leaf_type": 5, "score_deadline": 1762837200},
			      {"name": "第一章讨论", "leaf_id": "L3", "leaf_type": 4, "end_time": 1762923600}
			    ]
			  }]}
			}`))
		case "C2":
			_, _ = w.Write([]byte(`{
			  "success": true, "errcode": 0,
			  "data": {"chapters": [{
			    "name": "矩阵",
			    "children": [
				{"name": "期中考试", "leaf_id": "L9", "leaf_type": 5, "score_deadline": 1763010000,
				  "exam": {"exam_id": "E9"}, "chapter_id": "CH9"}
			    ]
			  }]}
			}`))
		default:
			http.NotFound(w, r)
		}
	})

	// 叶子详情：L2 标题更准确；L9 标题更准确。
	//
	// 字段名以实测响应为准：标题是 name（不是 title），
	// 且**没有** is_finish（完成状态由学习日志的 status 提供）。
	mux.HandleFunc("/mooc-api/v1/lms/learn/leaf_info/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case containsPath(r.URL.Path, "C1/L2"):
			_, _ = w.Write([]byte(`{
			  "success": true, "errcode": 0,
			  "data": {"name": "第一章作业（已批改）", "score_deadline": 1762837200}
			}`))
		case containsPath(r.URL.Path, "C2/L9"):
			_, _ = w.Write([]byte(`{
			  "success": true, "errcode": 0,
			  "data": {"name": "线性代数期中考试", "score_deadline": 1763010000}
			}`))
		default:
			_, _ = w.Write([]byte(`{"success": true, "errcode": 0, "data": {}}`))
		}
	})

	srv := newFakeServer(t, mux)

	client, err := New(Options{
		BaseURL:     srv.URL,
		Identity:    2,
		Concurrency: 2,
		ChapterTTL:  time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}

	tasks, stats, err := client.FetchAll(context.Background(), 30)
	if err != nil {
		t.Fatalf("抓取失败: %v", err)
	}

	if stats.Courses != 2 {
		t.Errorf("课程数 = %d, want 2", stats.Courses)
	}
	if stats.Leaves != 4 {
		t.Errorf("叶子数 = %d, want 4", stats.Leaves)
	}
	if len(tasks) != 4 {
		t.Fatalf("任务数 = %d, want 4", len(tasks))
	}

	byID := map[string]domain.Task{}
	for _, task := range tasks {
		byID[task.ID] = task
	}

	// 视频类：类型识别正确，且**不应**带截止时间。
	//
	// 原因：雨课堂给视频节点填的 score_deadline/end_time 是"整门课程的
	// 结束时间"，若当作任务截止会让 UI 显示"剩余 85 天"这类误导信息，
	// 也会在期末批量误报。因此 mapper 主动清空视频类的 Deadline。
	video, ok := byID["ykt:C1:L1"]
	if !ok {
		t.Fatalf("缺少 L1，实际: %v", keysOf(byID))
	}
	if video.Type != domain.TaskVideo {
		t.Errorf("L1 类型 = %v, want video", video.Type)
	}
	if !video.Deadline.IsZero() {
		t.Errorf("视频类不应带截止时间，实际 %v", video.Deadline)
	}

	// 作业类：详情覆盖标题
	hw, ok := byID["ykt:C1:L2"]
	if !ok {
		t.Fatalf("缺少 L2，实际: %v", keysOf(byID))
	}
	if hw.Type != domain.TaskHomework {
		t.Errorf("L2 类型 = %v, want homework", hw.Type)
	}
	if hw.Title != "第一章作业（已批改）" {
		t.Errorf("L2 标题应被详情覆盖，实际 %q", hw.Title)
	}
	// 详情接口不提供完成状态（真实响应无 is_finish），本 fixture 也没有
	// 学习日志，因此状态应为 unknown。完成状态由 learnlog_test.go 单独覆盖。
	if hw.Status != domain.StatusUnknown {
		t.Errorf("无学习日志时状态应为 unknown，实际 %v", hw.Status)
	}
	if hw.Course != "高等数学" {
		t.Errorf("L2 课程名 = %q", hw.Course)
	}

	// 讨论类
	if disc, ok := byID["ykt:C1:L3"]; !ok {
		t.Error("缺少 L3")
	} else if disc.Type != domain.TaskDiscuss {
		t.Errorf("L3 类型 = %v, want discuss", disc.Type)
	}

	// 考试类
	if exam, ok := byID["ykt:C2:L9"]; !ok {
		t.Error("缺少 L9")
	} else {
		if exam.Type != domain.TaskExam {
			t.Errorf("L9 类型 = %v, want exam", exam.Type)
		}
		if exam.Title != "线性代数期中考试" {
			t.Errorf("L9 标题 = %q", exam.Title)
		}
	}

	// 直达链接：不同类型应落到各自的路由，而不是全部退回首页。
	// 规则依据见 endpoints.go 的路由常量注释：
	//   - 章节内浏览走 ai-workspace（参数是章节树节点 id）
	//   - 考试走老版 /v2/web/exam/{cid}/{exam_id}
	// 这个 fixture 里 C2/L9 带 exam.exam_id=E9，因此应生成 exam 路由。
	wantURLs := map[string]string{
		"ykt:C1:L1": srv.URL + "/ai-workspace/lms-graph/C1/video/L1?is_chapter=1",
		"ykt:C1:L2": srv.URL + "/ai-workspace/lms-graph/C1/quiz/L2?is_chapter=1",
		"ykt:C1:L3": srv.URL + "/ai-workspace/lms-graph/C1/forum/L3?is_chapter=1",
		"ykt:C2:L9": srv.URL + "/v2/web/exam/C2/E9",
	}
	for id, want := range wantURLs {
		task, ok := byID[id]
		if !ok {
			t.Errorf("校验链接时缺少 %s", id)
			continue
		}
		if task.URL != want {
			t.Errorf("%s URL = %q, want %q", id, task.URL, want)
		}
		if !strings.HasPrefix(task.URL, "http") {
			t.Errorf("%s URL 应为绝对链接: %q", id, task.URL)
		}
	}
}

func TestFetchAllAuthExpired(t *testing.T) {
	srv := newFakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 模拟 cookie 失效：重定向到登录页
		http.Redirect(w, r, "/login", http.StatusFound)
	}))

	client, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = client.FetchAll(context.Background(), 7)
	if err == nil {
		t.Fatal("应返回错误")
	}
	if !IsAuthError(err) {
		t.Errorf("应识别为登录失效，实际: %v", err)
	}
}

func TestFetchAllRateLimited(t *testing.T) {
	srv := newFakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))

	client, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = client.FetchAll(context.Background(), 7)
	if err == nil {
		t.Fatal("应返回错误")
	}
	if !IsTransient(err) {
		t.Errorf("429 应被识别为可重试，实际: %v", err)
	}
}

func TestChapterCache(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/mooc-api/v1/lms/learn/course/chapter", func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"success":true,"errcode":0,"data":{"chapters":[]}}`))
	})
	srv := newFakeServer(t, mux)

	client, err := New(Options{BaseURL: srv.URL, ChapterTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := client.Chapter(ctx, "C1"); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Errorf("章节树应命中缓存，实际请求 %d 次", calls)
	}

	// 失效缓存后应重新请求
	client.invalidateChapterCache()
	if _, err := client.Chapter(ctx, "C1"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("缓存失效后应重新请求，实际 %d 次", calls)
	}
}

func TestPartialCourseFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/api/web/courses/list", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"errcode":0,"data":{"courses":[
		  {"classroom_id":"OK","name":"正常课程"},
		  {"classroom_id":"BAD","name":"异常课程"}
		]}}`))
	})
	mux.HandleFunc("/mooc-api/v1/lms/learn/course/chapter", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cid") == "BAD" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"errcode":0,"data":{"chapters":[
		  {"name":"作业","leaf_id":"L1","leaf_type":5,"score_deadline":1762837200}
		]}}`))
	})
	mux.HandleFunc("/mooc-api/v1/lms/learn/leaf_info/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"errcode":0,"data":{"title":"作业一"}}`))
	})
	srv := newFakeServer(t, mux)

	client, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	tasks, _, err := client.FetchAll(context.Background(), 7)
	if err == nil {
		t.Error("应汇报部分失败")
	}
	if len(tasks) != 1 {
		t.Errorf("正常课程的任务仍应返回，实际 %d 条", len(tasks))
	}
}

func containsPath(path, sub string) bool {
	for i := 0; i+len(sub) <= len(path); i++ {
		if path[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func keysOf(m map[string]domain.Task) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
