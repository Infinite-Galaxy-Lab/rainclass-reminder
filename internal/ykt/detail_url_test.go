package ykt

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestDetailRefillKeepsNodeID 是 bug #23 / #25 的完整链路回归测试。
//
// 背景：URL 在两条路径上生成——toTask（首次）和详情回填后（第二次）。
// 早期只有前者用 node id，后者用了 leafinfo_id，于是"修好了看起来还是错"。
// 脱敏复现：《第三章习题》node_id=80000023、leafinfo_id=80000024，
// 用户点到的页面是 quiz/80000024 →「此内容已被老师删除」。
//
// 后续（v0.6.6）：发现详情接口 **本身** 也要用 node id，于是同一个
// 混用错误在两条路径上各犯了一次。本测试同时锁定两处：
//   - 详情请求路径参数 = node id（不是 leafinfo_id）
//   - 生成的网页链接 = node id（不是 leafinfo_id）
//
// 这个测试的 fixture 刻意让 id 与 leafinfo_id **不同**，
// 否则测试会假通过（和之前 cookie jar 那次一样的坑）。
func TestDetailRefillKeepsNodeID(t *testing.T) {
	const (
		nodeID     = "80000023"
		leafInfoID = "80000024"
		classroom  = "80000008"
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/api/web/courses/list", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":0,"data":{"list":[{"classroom_id":"` + classroom + `","name":"概率论"}]}}`))
	})
	// 章节树：id 与 leafinfo_id 是两个不同字段（实测结构）。
	mux.HandleFunc("/mooc-api/v1/lms/learn/course/chapter", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"errcode":0,"data":{"course_chapter":[{
		  "id": 7000009, "name": "第三章",
		  "section_leaf_list": [{
		    "id": ` + nodeID + `, "leafinfo_id": ` + leafInfoID + `,
		    "leaf_type": 5, "name": "《第三章习题》",
		    "score_deadline": 1762837200000
		  }]
		}]}}`))
	})
	// 详情：路径参数必须是**节点 id**，且必须带 classroom-id 头。
	//
	// 这条断言在 v0.6.6 被修正过：旧版本写的是"详情接口应使用 leafinfo_id"，
	// 而实测 40 个叶子用 leafinfo_id 请求 100% 返回 404000
	// "Objects does not exist."。同一个 ID 混用错误（bug #23 只修了链接，
	// 没顺着查详情接口）导致标题精修与完成状态长期静默失效（bug #25）。
	var requestedPath string
	var gotClassroomHeader string
	mux.HandleFunc("/mooc-api/v1/lms/learn/leaf_info/", func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		gotClassroomHeader = r.Header.Get("classroom-id")
		_, _ = w.Write([]byte(`{"success":true,"errcode":0,"data":{"name":"《第三章习题》","score_deadline":1762837200000}}`))
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
	if stats.DetailsFetched != 1 {
		t.Fatalf("应拉取 1 个详情，实际 %d", stats.DetailsFetched)
	}

	// 详情接口必须用节点 id，且带上 classroom-id 头。
	if !strings.Contains(requestedPath, nodeID) {
		t.Errorf("详情请求应使用节点 id，实际路径 %q", requestedPath)
	}
	if strings.Contains(requestedPath, leafInfoID) {
		t.Errorf("详情请求不能用 leafinfo_id（会返回 404000），实际路径 %q", requestedPath)
	}
	if gotClassroomHeader != classroom {
		t.Errorf("详情请求应带 classroom-id 头，实际 %q", gotClassroomHeader)
	}

	if len(tasks) != 1 {
		t.Fatalf("应得到 1 个任务，实际 %d", len(tasks))
	}
	got := tasks[0].URL
	want := srv.URL + "/ai-workspace/lms-graph/" + classroom + "/quiz/" + nodeID + "?is_chapter=1"
	if got != want {
		t.Errorf("详情回填后 URL 被改坏：\n got %q\nwant %q", got, want)
	}
	if strings.Contains(got, leafInfoID) {
		t.Errorf("URL 里不能出现 leafinfo_id（会打开“内容已被老师删除”）：%q", got)
	}
}
