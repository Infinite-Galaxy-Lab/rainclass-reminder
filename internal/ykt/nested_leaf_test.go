package ykt

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestNestedLeafListExpanded 是 v0.6.3 的核心回归测试。
//
// 脱敏结构（课程 80000001，实测 2026-11）：
//
//	section_leaf_list: [
//	  { "id": 7000010, "name": "1-1 入门案例", "leaf_type": 0,
//	    "leafinfo_id": null,            ← 这是"节"，不是叶子
//	    "leaf_list": [
//	      {"id": 80000014, "leafinfo_id": 80000017, "name": "1-1-1：案例…"},
//	      {"id": 80000015, "leafinfo_id": 80000018, "name": "1-1-2：搜索…"}
//	    ]},
//	  { "id": 80000016, "leaf_type": 6, "name": "模块一作业" }  ← 直接的叶子
//	]
//
// 用户点开的真实页面是 /video/80000014?is_chapter=1&node_id=7000010。
// 早期实现只读了 section_leaf_list 一层，把"节"（7000010）当成任务，
// 生成的 /video/7000010 会打开空白内容区。
func TestNestedLeafListExpanded(t *testing.T) {
	const classroom = "80000001"

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/api/web/courses/list", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":0,"data":{"list":[{"classroom_id":"` + classroom + `","name":"信息素养"}]}}`))
	})
	mux.HandleFunc("/mooc-api/v1/lms/learn/course/chapter", func(w http.ResponseWriter, r *http.Request) {
		// section_leaf_list 的第一项是"节"（有 leaf_list），第二项是叶子。
		_, _ = w.Write([]byte(`{"success":true,"errcode":0,"data":{"course_chapter":[{
		  "id": 7000007, "name": "模块一",
		  "section_leaf_list": [
		    {
		      "id": 7000010, "name": "1-1 入门案例", "leaf_type": 0,
		      "section_id": null,
		      "leaf_list": [
		        {"id": 80000014, "leafinfo_id": 80000017, "leaf_type": 0,
		         "name": "1-1-1：案例：一分钟找出8000多个身份证号中的错误",
		         "section_id": 7000010, "score_deadline": 1783044900000},
		        {"id": 80000015, "leafinfo_id": 80000018, "leaf_type": 0,
		         "name": "1-1-2：搜索引擎的站内搜索语法",
		         "section_id": 7000010, "score_deadline": 1783044900000}
		      ]
		    },
		    {
		      "id": 80000016, "leafinfo_id": 80000019, "leaf_type": 6,
		      "name": "模块一作业", "score_deadline": 1783044900000
		    }
		  ]
		}]}}`))
	})
	mux.HandleFunc("/mooc-api/v1/lms/learn/leaf_info/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"errcode":0,"data":{}}`))
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

	tasks, _, err := client.FetchAll(context.Background(), 30)
	if err != nil {
		t.Fatalf("抓取失败: %v", err)
	}

	byID := make(map[string]struct {
		title, url string
	}, len(tasks))
	for _, tk := range tasks {
		byID[tk.ID] = struct {
			title, url string
		}{tk.Title, tk.URL}
	}

	// "节"本身绝不能成为任务。
	if _, exists := byID["ykt:80000001:7000010"]; exists {
		t.Error("“节”(7000010) 不应被当成任务，它是叶子容器")
	}

	// 真实的两个视频叶子都应在任务列表里。
	for _, want := range []struct{ id, titleFrag string }{
		{"ykt:80000001:80000014", "1-1-1"},
		{"ykt:80000001:80000015", "1-1-2"},
	} {
		got, ok := byID[want.id]
		if !ok {
			t.Errorf("缺少真实叶子 %s", want.id)
			continue
		}
		if !strings.Contains(got.title, want.titleFrag) {
			t.Errorf("%s 标题 = %q，应含 %q", want.id, got.title, want.titleFrag)
		}
		if !strings.Contains(got.url, "/video/"+strings.TrimPrefix(want.id, "ykt:80000001:")) {
			t.Errorf("%s 链接应指向该视频叶子，实际 %q", want.id, got.url)
		}
		if strings.Contains(got.url, "7000010") {
			t.Errorf("%s 链接不应指向“节”ID：%q", want.id, got.url)
		}
	}

	// 直接的叶子（作业）仍要保留。
	if _, ok := byID["ykt:80000001:80000016"]; !ok {
		t.Error("直接的叶子（作业 80000016）丢失")
	}
}

// TestEmptySectionSkipped 验证纯容器节点不会生成空任务。
func TestEmptySectionSkipped(t *testing.T) {
	const classroom = "C1"

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/api/web/courses/list", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":0,"data":{"list":[{"classroom_id":"` + classroom + `","name":"课"}]}}`))
	})
	mux.HandleFunc("/mooc-api/v1/lms/learn/course/chapter", func(w http.ResponseWriter, r *http.Request) {
		// 一个既无类型、无 leafinfo_id、无截止时间、也无 leaf_list 的容器。
		_, _ = w.Write([]byte(`{"success":true,"errcode":0,"data":{"course_chapter":[{
		  "id": 900, "name": "空章节",
		  "section_leaf_list": [
		    {"id": 901, "name": "空容器", "leaf_type": 0},
		    {"id": 902, "leafinfo_id": 9902, "leaf_type": 6, "name": "真作业"}
		  ]
		}]}}`))
	})
	mux.HandleFunc("/mooc-api/v1/lms/learn/leaf_info/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"errcode":0,"data":{}}`))
	})

	srv := newFakeServer(t, mux)
	client, err := New(Options{BaseURL: srv.URL, Identity: 2, ChapterTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}

	tasks, _, err := client.FetchAll(context.Background(), 30)
	if err != nil {
		t.Fatalf("抓取失败: %v", err)
	}
	for _, tk := range tasks {
		if tk.ID == "ykt:C1:901" {
			t.Error("空容器 901 不应成为任务")
		}
	}
	found := false
	for _, tk := range tasks {
		if tk.ID == "ykt:C1:902" {
			found = true
		}
	}
	if !found {
		t.Error("真作业 902 应保留")
	}
}
