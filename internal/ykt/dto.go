package ykt

import (
	"encoding/json"
	"fmt"
	"strings"
)

// DTO 层只描述"接口返回了什么"，不做业务判断。转换为 domain.Task 的
// 逻辑在 mapper.go。
//
// 标量一律用宽容类型接收，因为雨课堂在不同课程/版本下会给出不同类型。
// 字段结构以真实响应为准，与公开资料的差异：课程列表在 data.list、
// 章节树在 data.course_chapter[].section_leaf_list[]、时间戳为毫秒。

// APIEnvelope 是雨课堂大多数接口的统一信封。
//
// 成功：{"success": true, "data": {...}}
// 失败：{"code": 50000, "msg": "UNAUTHENTICATED", "data": ""}
//
// 失败格式与成功有三处差异，都要单独处理：错误码叫 code 而非 errcode，
// data 可能是空字符串而非对象，code=50000 表示未认证。
type APIEnvelope[T any] struct {
	// 用 FlexBool 而非 FlexInt，否则 true/false 都会被读成 0。
	Success FlexBool   `json:"success"`
	ErrCode FlexInt    `json:"errcode"`
	ErrMsg  FlexString `json:"errmsg"`

	Code FlexInt    `json:"code"`
	Msg  FlexString `json:"msg"`

	// ErrorCode 是 /mooc-api/* 系列的错误码字段名。
	// 缺了它，{"success":false,"error_code":40000} 会被判定为成功。
	ErrorCode FlexInt `json:"error_code"`

	Data T `json:"data"`

	// 记录 success 字段是否真的出现过（JSON 层未导出）。
	successSet bool
}

// UnmarshalJSON 宽容解析信封。
//
// data 成功时是对象，失败时是空字符串。直接让 encoding/json 处理会在
// 遇到 "" 时报类型不匹配，整个响应被丢弃。这里先用 RawMessage 接住，
// 再按需二次解析。
func (e *APIEnvelope[T]) UnmarshalJSON(b []byte) error {
	var raw struct {
		// 指针：nil 表示服务端没给这个字段，与"明确返回 false"不同。
		Success   *FlexBool       `json:"success"`
		ErrCode   FlexInt         `json:"errcode"`
		ErrMsg    FlexString      `json:"errmsg"`
		Code      FlexInt         `json:"code"`
		Msg       FlexString      `json:"msg"`
		ErrorCode FlexInt         `json:"error_code"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}

	if raw.Success != nil {
		e.Success = *raw.Success
		e.successSet = true
	}
	e.ErrCode = raw.ErrCode
	e.ErrMsg = raw.ErrMsg
	e.Code = raw.Code
	e.Msg = raw.Msg
	e.ErrorCode = raw.ErrorCode

	var zero T
	e.Data = zero
	if len(raw.Data) == 0 || string(raw.Data) == "null" {
		return nil
	}
	// data 不是对象（错误响应里是 ""）时保持零值，不让整体失败。
	if err := json.Unmarshal(raw.Data, &e.Data); err != nil {
		e.Data = zero
	}
	return nil
}

// OK 判断业务层是否成功。三个错误码字段都要看，success 显式 false 也算失败。
func (e APIEnvelope[T]) OK() bool {
	if e.ErrCode.Int64() != 0 || e.Code.Int64() != 0 || e.ErrorCode.Int64() != 0 {
		return false
	}
	// successSet 区分"明确返回 false"与"根本没这个字段"。
	if e.successSet && !e.Success.Bool() {
		return false
	}
	return true
}

// Message 返回服务端错误描述（兼容 errmsg / msg 两种字段名）。
func (e APIEnvelope[T]) Message() string {
	if s := e.ErrMsg.String(); s != "" {
		return s
	}
	return e.Msg.String()
}

// IsAuthFailure 判断该响应是否表示未登录。雨课堂用 code=50000 表达这个
// 语义且走 HTTP 200，只能在业务层识别。
func (e APIEnvelope[T]) IsAuthFailure() bool {
	if e.Code.Int64() == 50000 {
		return true
	}
	msg := strings.ToLower(e.Message())
	return strings.Contains(msg, "unauthenticated") || strings.Contains(msg, "not login")
}

// envelopeErr 把失败的信封转成统一错误类型，集中在各处调用以免漏掉规则。
func envelopeErr[T any](env APIEnvelope[T]) error {
	if env.OK() {
		return nil
	}
	if env.IsAuthFailure() {
		return ErrAuthExpired
	}
	return fmt.Errorf("%w: %s", ErrUpstream, env.Message())
}

// BasicInfoData 是 /api/v3/user/basic-info 的 data 段。
type BasicInfoData struct {
	UserID   FlexString `json:"user_id"`
	Nickname FlexString `json:"nickname"`
	Name     FlexString `json:"name"`
	School   FlexString `json:"school"`
}

// CourseListData 是课程列表的 data 段，兼容三个历史字段名。
type CourseListData struct {
	List          []CourseItem `json:"list"`
	Courses       []CourseItem `json:"courses"`
	ClassroomList []CourseItem `json:"classroom_list"`
}

// Items 合并三种可能的字段并按 classroom_id 去重。
func (d CourseListData) Items() []CourseItem {
	out := make([]CourseItem, 0, len(d.List)+len(d.Courses)+len(d.ClassroomList))
	out = append(out, d.List...)
	out = append(out, d.Courses...)
	out = append(out, d.ClassroomList...)
	return dedupeCourses(out)
}

func dedupeCourses(in []CourseItem) []CourseItem {
	seen := make(map[string]bool, len(in))
	out := make([]CourseItem, 0, len(in))
	for _, c := range in {
		id := c.ClassroomID.String()
		if id == "" {
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, c)
	}
	return out
}

// CourseItem 是一门课程（课堂）。
type CourseItem struct {
	ClassroomID FlexString `json:"classroom_id"`
	CourseID    FlexString `json:"course_id"`
	Name        FlexString `json:"name"`
	ShortName   FlexString `json:"short_name"`
	ClassName   FlexString `json:"class_name"`
	Title       FlexString `json:"title"`
	Teacher     struct {
		Name FlexString `json:"name"`
	} `json:"teacher"`
}

// DisplayName 返回课程展示名，兼容多个可能字段。
func (c CourseItem) DisplayName() string {
	for _, v := range []FlexString{c.Name, c.Title, c.ClassName, c.ShortName} {
		if v.String() != "" {
			return v.String()
		}
	}
	return "未命名课程"
}

// ChapterData 是章节树的 data 段。
type ChapterData struct {
	CourseID      FlexString    `json:"course_id"`
	CourseName    FlexString    `json:"course_name"`
	CourseChapter []ChapterNode `json:"course_chapter"`

	// 兼容历史结构。
	Chapters []ChapterNode `json:"chapters"`
	LeafInfo []LeafNode    `json:"leafinfo"`
}

// ChapterNode 是章节，内含叶子列表。
type ChapterNode struct {
	ID       FlexString    `json:"id"`
	Name     FlexString    `json:"name"`
	Order    FlexInt       `json:"order"`
	Children []ChapterNode `json:"children"`

	SectionLeafList []LeafNode `json:"section_leaf_list"`

	// 兼容：某些版本章节与叶子同构，叶子字段直接挂在章节上。
	LeafID        FlexString `json:"leaf_id"`
	LeafInfoID    FlexString `json:"leafinfo_id"`
	LeafType      FlexInt    `json:"leaf_type"`
	ScoreDeadline FlexTime   `json:"score_deadline"`
	EndTime       FlexTime   `json:"end_time"`
	StartTime     FlexTime   `json:"start_time"`
	Status        FlexString `json:"status"`
	HaveRead      FlexInt    `json:"have_read"`
	// forum 路由的 node_id 查询参数取的就是 chapter_id。
	ChapterID FlexString `json:"chapter_id"`
	Exam      *ExamMeta  `json:"exam"`
}

// LeafNode 是叶子（任务节点）。
type LeafNode struct {
	// ID 是叶子节点 ID，网页端路由与详情接口都用它。
	ID FlexString `json:"id"`
	// LeafInfoID 是与 ID 不同的另一个字段，不要混用。
	LeafInfoID FlexString `json:"leafinfo_id"`
	LeafID     FlexString `json:"leaf_id"`
	LeafType   FlexInt    `json:"leaf_type"`
	Name       FlexString `json:"name"`

	ScoreDeadline FlexTime `json:"score_deadline"`
	EndTime       FlexTime `json:"end_time"`
	StartTime     FlexTime `json:"start_time"`

	ChapterID FlexString `json:"chapter_id"`
	// SectionID 是所属"节"的 ID。课程可能是「章 → 节 → 叶子」三层。
	SectionID FlexString `json:"section_id"`
	Order     FlexInt    `json:"order"`

	// LeafList 是"节"下面的真实叶子。遍历时必须优先下钻它，
	// 只有它为空时当前节点自己才是叶子。
	LeafList []LeafNode `json:"leaf_list"`

	Status   FlexString `json:"status"`
	IsShow   FlexInt    `json:"is_show"`
	IsLocked FlexInt    `json:"is_locked"`
	IsScore  FlexInt    `json:"is_score"`
	Exam     *ExamMeta  `json:"exam"`

	IsFinish FlexInt `json:"is_finish"`
}

// ExamMeta 是叶子上的考试元信息。
type ExamMeta struct {
	OnlineProctor FlexInt    `json:"online_proctor"`
	IsOffline     FlexInt    `json:"is_offline"`
	ExamID        FlexString `json:"exam_id"`
}

// LearnLogData 是学习日志接口的 data 段。
//
// 这是唯一能拿到考试/习题真实 ID 的接口：章节树的 exam.exam_id 全站为空。
type LearnLogData struct {
	Activities []LearnLogActivity `json:"activities"`
	HasMore    bool               `json:"has_more"`
}

// LearnLogActivity 是一条学习日志（一次课件/试卷交互）。
type LearnLogActivity struct {
	// Type 决定详情页路由：4 习题、5 考试、9 公告、14 视频。
	Type FlexInt `json:"type"`
	// ID 是这条日志本身的 ID，不是试卷 ID。
	ID FlexInt `json:"id"`
	// CoursewareID 是试卷/考试的真实 ID，即网页端路由里的那一段。
	CoursewareID FlexString `json:"courseware_id"`
	Title        FlexString `json:"title"`
	CreateTime   FlexTime   `json:"create_time"`
	Deadline     FlexTime   `json:"deadline"`
	TotalScore   OptFloat   `json:"total_score"`
	ProblemCount FlexInt    `json:"problem_count"`
	// 完成状态：0/1 未完成、3 已完成。用 OptInt 而非 FlexInt，
	// 因为 0 是合法取值，必须与"字段缺失"区分开。
	Status OptInt   `json:"status"`
	Score  OptFloat `json:"score"`
}

// OptInt 是"可缺省整数"，区分字段不存在与值为 0。
type OptInt struct {
	V   int64
	Set bool
}

// UnmarshalJSON 实现宽容解析，同时置位 Set。
func (o *OptInt) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == `""` || s == "" {
		o.V, o.Set = 0, false
		return nil
	}
	o.Set = true
	var f FlexInt
	if err := f.UnmarshalJSON(b); err != nil {
		o.V = 0
		return nil // 宽容：不因单个字段报错
	}
	o.V = f.Int64()
	return nil
}

// Int64 返回数值（缺失时为 0）。
func (o OptInt) Int64() int64 { return o.V }

// Defined 表示字段是否真实出现过。
func (o OptInt) Defined() bool { return o.Set }

// LearnLogType 是学习日志里的活动类型常量。
const (
	LearnLogQuiz  = 4 // 习题/随堂测验
	LearnLogExam  = 5 // 考试
	LearnLogNote  = 9 // 公告
	LearnLogVideo = 14
)

// LeafInfoData 是叶子详情的 data 段。
//
// 只保留确证存在的字段：真实响应里没有 is_finish / status / score_status /
// exam_id，完成状态由学习日志提供。
type LeafInfoData struct {
	LeafID   FlexString `json:"id"`
	LeafType FlexInt    `json:"leaf_type"`
	Title    FlexString `json:"name"`
	// ScoreDeadline 是任务自身的截止时间；ClassEndTime 是整门课的结束时间，
	// 两者不同（《第一章习题》sd=2026-10-15、ce=2027-01-31）。
	ScoreDeadline FlexTime `json:"score_deadline"`
	PublishTime   FlexTime `json:"publish_time"`
	ClassEndTime  FlexTime `json:"class_end_time"`
	// ClassStartTime 是该课时段的开始时间（课堂类任务的提醒依据）。
	ClassStartTime FlexTime `json:"class_start_time"`
	// ForceReason 是服务端给出的状态文案，实测取值含"已截止考核"，
	// 未截止时为 null。它表达"还能不能提交"，不是"做没做完"。
	ForceReason FlexString `json:"force_reason"`
	IsLocked    FlexBool   `json:"is_locked"`
	IsDeleted   FlexBool   `json:"is_deleted"`
	// IsAssessed 表示该叶子是否计入成绩。
	IsAssessed FlexBool `json:"is_assessed"`
	// ContentInfo 承载服务端给出的内容类型标签。
	ContentInfo LeafContentInfo `json:"content_info"`
}

// LeafContentInfo 是详情响应里的内容信息段。
type LeafContentInfo struct {
	// ScoreEvaluation.Name 是唯一可靠的类型来源：leaf_type 在同一门课内
	// 也会漂移，而这个标签始终准确。
	// 实测取值：视频 / 图文 / 讨论 / 作业 / 考试 / 课堂 / 雨课堂课件。
	ScoreEvaluation struct {
		Name FlexString `json:"name"`
	} `json:"score_evaluation"`
}

// ContentLabel 返回服务端给出的内容类型标签（可能为空）。
func (d LeafInfoData) ContentLabel() string {
	return strings.TrimSpace(d.ContentInfo.ScoreEvaluation.Name.String())
}

// ExamCoverData 是 /v/exam/cover 的 data 段。
type ExamCoverData struct {
	ExamID        FlexString `json:"exam_id"`
	Title         FlexString `json:"title"`
	Name          FlexString `json:"name"`
	Deadline      FlexTime   `json:"deadline"`
	StartTime     FlexTime   `json:"start_time"`
	ScoreDeadline FlexTime   `json:"score_deadline"`
	// 考试时长（分钟），用于推算最晚开始时间。
	Limit FlexInt `json:"limit"`
	// Result 是本人考试状态。
	Result struct {
		Status FlexString `json:"status"`
		Score  FlexString `json:"score"`
	} `json:"result"`
	ServerTime FlexTime `json:"server_time"`
}

// TitleText 返回考试标题。
func (d ExamCoverData) TitleText() string {
	if s := d.Title.String(); s != "" {
		return s
	}
	return d.Name.String()
}
