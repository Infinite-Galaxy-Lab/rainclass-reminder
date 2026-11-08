package ykt

import (
	"encoding/json"
	"testing"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/domain"
)

// TestCoursewareRealPayload 用真实响应跑一遍整条链路，定位
// "计分课件没有被提升为作业"的原因。
func TestCoursewareRealPayload(t *testing.T) {
	// 这是 /leaf_info/23175789/45030169/ 的真实响应的关键字段（2026-11-08）
	raw := `{
	  "id": 45030169,
	  "name": "作业1单元",
	  "leaf_type": 7,
	  "is_assessed": true,
	  "is_score": true,
	  "is_locked": false,
	  "is_deleted": false,
	  "score_deadline": 1778428740000,
	  "class_start_time": 1777982341000,
	  "content_info": {
	    "status": "post",
	    "is_score": true,
	    "score_evaluation": {
	      "name": "雨课堂课件",
	      "score": 0.0,
	      "score_proportion": {"proportion": 0.05}
	    }
	  }
	}`

	var d LeafInfoData
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	t.Logf("name=%q leaf_type=%d is_assessed=%v score_deadline=%d label=%q",
		d.Title.String(), d.LeafType.Int64(), d.IsAssessed.Bool(),
		d.ScoreDeadline.Unix(), d.ContentLabel())

	if got := d.ContentLabel(); got != "雨课堂课件" {
		t.Fatalf("标签解析失败: %q", got)
	}
	if !d.IsAssessed.Bool() {
		t.Fatal("is_assessed 解析失败")
	}
	if d.ScoreDeadline.Unix() == 0 {
		t.Fatal("score_deadline 解析失败")
	}

	// 业务类型应当被提升为作业
	if got := taskTypeFromInfo(d); got != domain.TaskHomework {
		t.Errorf("taskTypeFromInfo = %v, want homework", got)
	}

	// 走完整 applyLeafInfo：从 video 起始状态应变成 homework 且有截止时间
	start := domain.Task{
		ID:     "ykt:23175789:45030169",
		Type:   domain.TaskVideo,
		Title:  "作业1单元",
		Status: domain.StatusUnknown,
		Source: "chapter:leaf_type=7",
	}
	got := applyLeafInfo(start, d)
	t.Logf("after applyLeafInfo: type=%v deadline=%v", got.Type, got.Deadline)
	if got.Type != domain.TaskHomework {
		t.Errorf("类型应提升为 homework，实际 %v", got.Type)
	}
	if got.Deadline.IsZero() {
		t.Error("截止时间应被写入")
	}
}
