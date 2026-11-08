package ykt

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/domain"
)

// 本文件锁定 v0.6.6 修的四个 P0 bug（#25 / #26）。全部离线纯函数或
// httptest，不依赖网络。
//
// 背景：v0.6.5 之前，面板上 464 个任务的完成状态**全部**是 unknown。
// 顺着这个现象查下去，发现详情接口从来没成功过——参数传错（用了
// leafinfo_id 而不是节点 id）、请求头缺失（没带 classroom-id）、
// DTO 字段名靠猜（读 is_finish，真实响应没有），三层错误叠加，
// 而每一层都"HTTP 200"，所以谁也没发现。

// TestFlexBoolParsesJSONBooleans 锁定新增的 FlexBool。
//
// 存在的理由：is_locked / is_deleted / is_assessed 在真实响应里是
// JSON 布尔值。用 FlexInt 接收时，true 和 false 都会被 ParseInt 失败
// 后退化成 0，两者无法区分——这正是"读到了字段却读不出值"的典型形态。
func TestFlexBoolParsesJSONBooleans(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{`true`, true},
		{`false`, false},
		{`1`, true},
		{`0`, false},
		{`"true"`, true},
		{`"false"`, false},
		{`null`, false},
	}
	for _, tc := range cases {
		var b FlexBool
		if err := json.Unmarshal([]byte(tc.raw), &b); err != nil {
			t.Fatalf("解析 %q 失败: %v", tc.raw, err)
		}
		if b.Bool() != tc.want {
			t.Errorf("%q 解析为 %v, want %v", tc.raw, b.Bool(), tc.want)
		}
	}
}

// TestOptIntDistinguishesZeroFromMissing 锁定 status 的"0 合法"语义。
//
// 学习日志里 status=0 是真实取值（未完成），必须和"字段不存在"
// 区分开，否则没交的作业会被当成"状态未知"，退化成 v0.6.5 的老问题。
func TestOptIntDistinguishesZeroFromMissing(t *testing.T) {
	var missing OptInt
	if err := json.Unmarshal([]byte(`null`), &missing); err != nil {
		t.Fatal(err)
	}
	if missing.Defined() {
		t.Error("null 应视为字段缺失")
	}

	var zero OptInt
	if err := json.Unmarshal([]byte(`0`), &zero); err != nil {
		t.Fatal(err)
	}
	if !zero.Defined() {
		t.Error("0 是合法取值，不能当成缺失")
	}
	if zero.Int64() != 0 {
		t.Errorf("值应为 0，实际 %d", zero.Int64())
	}

	var three OptInt
	if err := json.Unmarshal([]byte(`"3"`), &three); err != nil {
		t.Fatal(err)
	}
	if !three.Defined() || three.Int64() != 3 {
		t.Errorf("字符串 \"3\" 应解析为 3，实际 %d defined=%v", three.Int64(), three.Defined())
	}
}

// TestLearnLogStatusMapping 锁定学习日志 status 的语义。
//
// 实测依据（2026-11-08，65 条作业/考试日志逐条比对 score）：
//
//	status=3 → score 有值（含 0/4 这种低分）→ 已完成
//	status=0 → score 为空                → 未完成
//	status=1 → score 为空                → 未完成
//
// 关键点：**3 是"已完成"不是因为分数高，而是因为服务端给出了成绩**。
// 0/4 考了 0 分也算完成，这个 case 必须锁住，否则会被改成"看分数判断"。
func TestLearnLogStatusMapping(t *testing.T) {
	cases := []struct {
		status  int64
		defined bool
		want    domain.TaskStatus
	}{
		{3, true, domain.StatusDone},
		{1, true, domain.StatusPending},
		{0, true, domain.StatusPending},
		{0, false, domain.StatusUnknown},
		{3, false, domain.StatusUnknown},
	}
	for _, tc := range cases {
		got := learnLogStatus(tc.status, tc.defined)
		if got != tc.want {
			t.Errorf("learnLogStatus(%d, defined=%v) = %v, want %v",
				tc.status, tc.defined, got, tc.want)
		}
	}
}

// TestLeafInfoUsesNodeIDAndClassroomHeader 是 bug #25 的核心回归测试。
//
// 三方证据：
//   - 无 classroom-id 头 → HTTP 200 + error_code 40000 CLASSROOM ID IS REQUIRED
//   - 用 leafinfo_id      → HTTP 200 + error_code 404000 Objects does not exist
//   - 用节点 id + 头      → success=true
//
// 服务端就连失败都返回 HTTP 200，所以只看状态码永远发现不了。
func TestLeafInfoUsesNodeIDAndClassroomHeader(t *testing.T) {
	const (
		classroom   = "25570088"
		nodeID      = "48057154"
		leafInfoID  = "48059375"
		expectedURL = "/mooc-api/v1/lms/learn/leaf_info/" + classroom + "/" + nodeID + "/"
	)

	mux := http.NewServeMux()
	var seenPath, seenClassroom string
	mux.HandleFunc("/mooc-api/v1/lms/learn/leaf_info/", func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenClassroom = r.Header.Get("classroom-id")
		// 模拟真实服务端：参数或头不对时返回 HTTP 200 + 业务错误码。
		if seenClassroom == "" {
			_, _ = w.Write([]byte(`{"success":false,"error_code":40000,"msg":"CLASSROOM ID IS REQUIRED","data":{}}`))
			return
		}
		if seenPath != expectedURL {
			_, _ = w.Write([]byte(`{"success":false,"error_code":404000,"msg":"Objects does not exist.","data":{}}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"error_code":0,"data":{"id":` + nodeID + `,"name":"《第一章习题》","score_deadline":1792079999000,"leaf_type":5}}`))
	})

	srv := newFakeServer(t, mux)
	c, err := New(Options{BaseURL: srv.URL, ChapterTTL: time.Minute, MinRequestGap: 0})
	if err != nil {
		t.Fatal(err)
	}

	info, err := c.LeafInfo(context.Background(), classroom, nodeID)
	if err != nil {
		t.Fatalf("详情请求应成功，实际: %v", err)
	}
	if info.Title.String() != "《第一章习题》" {
		t.Errorf("标题 = %q", info.Title.String())
	}
	if seenClassroom != classroom {
		t.Errorf("classroom-id 头 = %q, want %q", seenClassroom, classroom)
	}
	if seenPath == "" || seenPath[len(seenPath)-1] != '/' {
		t.Errorf("详情路径应以 / 结尾：%q", seenPath)
	}
	// 传错的 ID 会拿到 40000/404000，这时应当被识别为业务错误而非成功。
	if _, err := c.LeafInfo(context.Background(), classroom, leafInfoID); err == nil {
		t.Error("用 leafinfo_id 请求应报错（服务端返回 404000），不能静默成功")
	}
}

// TestLeafInfoMissingHeaderIsError 锁定"HTTP 200 + 业务错误码"必须报错。
//
// 这是本轮最隐蔽的一层：如果 HTTP 层不报错、业务层也不报错，
// 调用方就会拿着一个空结构体继续往下走，表现为"字段全是零值"。
func TestLeafInfoMissingHeaderIsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mooc-api/v1/lms/learn/leaf_info/", func(w http.ResponseWriter, r *http.Request) {
		// 即使客户端该带头，这里也强制返回缺头错误，模拟服务端收紧。
		_, _ = w.Write([]byte(`{"success":false,"error_code":40000,"msg":"CLASSROOM ID IS REQUIRED","data":{}}`))
	})
	srv := newFakeServer(t, mux)

	// 故意用空 classroomID 触发"不带头"的路径。
	c, err := New(Options{BaseURL: srv.URL, MinRequestGap: 0})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.LeafInfo(context.Background(), "", "48057154")
	if err == nil {
		t.Fatal("服务端返回 40000 时必须报错，否则调用方会拿着空结构体继续跑")
	}
}
