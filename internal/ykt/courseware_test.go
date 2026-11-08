package ykt

import (
	"encoding/json"
	"testing"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/domain"
)

// TestCoursewareRealPayloadStaysVideo 确认计分/考核字段不会把课件变成提交任务。
func TestCoursewareRealPayloadStaysVideo(t *testing.T) {
	// 这是 /leaf_info/80000004/80000013/ 的脱敏响应的关键字段（2026-11-08）
	raw := `{
	  "id": 80000013,
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

	if got := taskTypeFromInfo(d); got != domain.TaskVideo {
		t.Errorf("taskTypeFromInfo = %v, want video", got)
	}

	leaf := candidateLeaf{
		classroom: "80000004",
		leafID:    "80000013",
		chapterID: "80000003",
		title:     "作业1单元",
		leafType:  7,
		deadline:  d.ScoreDeadline.Unix(),
	}
	start := toTask(leaf, testBase)
	got := applyLeafInfo(start, d)
	t.Logf("after applyLeafInfo: type=%v deadline=%v", got.Type, got.Deadline)
	if got.Type != domain.TaskVideo {
		t.Errorf("课件应保持 video，实际 %v", got.Type)
	}
	if got.HasDeadline() {
		t.Errorf("阅读材料不应保留考核截止时间，实际 %v", got.Deadline)
	}
	got.URL = leafURLWithLabel(testBase, leaf, d.ContentLabel())
	wantURL := testBase + "/ai-workspace/lms-graph/80000004/video/80000013?is_chapter=1"
	if got.URL != wantURL {
		t.Errorf("课件 URL = %q, want %q", got.URL, wantURL)
	}
}
