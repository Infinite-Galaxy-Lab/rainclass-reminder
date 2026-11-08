package ykt

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/2963609262/ykt-reminder/internal/domain"
)

// 本文件是 DTO → domain.Task 的唯一转换点。
// 雨课堂字段的语义知识集中在这里，上层不感知。

// leafTypeMap 是业务类型的兜底表，仅在服务端标签与标题关键词都无命中时使用。
//
// 业务类型（本表）与路由（chapterSegment）是两件独立的事：
// 前者决定显示与提醒策略，后者决定页面挂在哪个组件下。两者不必一致。
// 例如 leaf_type=7 的「期中2026」业务上是考试（该提醒），
// 但服务端标签是"雨课堂课件"，只能走 video 路由。
//
// 依据服务端 content_info.score_evaluation.name 逐条比对得出：
//
//	leaf_type   服务端标签        样本
//	0           视频              35
//	3           图文              31
//	4           讨论              10
//	5           作业 / 考试        4
//	7           雨课堂课件         21
//	8           课堂              37
//	9           考试              20
var leafTypeMap = map[int64]domain.TaskType{
	0: domain.TaskVideo,
	3: domain.TaskVideo,
	4: domain.TaskDiscuss,
	5: domain.TaskHomework,
	6: domain.TaskHomework,
	7: domain.TaskHomework,
	8: domain.TaskVideo,
	9: domain.TaskHomework,
}

// LeafTypeDiscussGroup 是"讨论/小组作业"的 leaf_type。
//
// 单列是因为这类标题常带"作业"二字（如「小组作业——高斯分布的应用」），
// 走关键词优先会被误判成作业并生成 /quiz 链接，而它在网页端挂在 /forum 下。
const LeafTypeDiscussGroup = 4

// 名称关键词规则。顺序有意义：越具体的越靠前。
// 这些词来自真实课程标题（如"随堂测验2.4"、"小组作业"、"期中2026"）。
var (
	examKeywords     = []string{"考试", "期中", "期末", "补考"}
	discussKeywords  = []string{"讨论", "研讨"}
	homeworkKeywords = []string{"作业", "习题", "练习", "测验", "测试", "模拟题", "单元"}
	videoKeywords    = []string{"视频", "课件", "课程简介"}
)

// classify 结合名称关键词与 leaf_type 判定业务类型。
//
// 业务类型只影响展示标签与是否按截止时间提醒，不决定路由。
//
// 判定顺序：
//  1. leaf_type=4 已能锁定语义，不让标题改写；
//  2. 名称关键词优先（考试 > 讨论 > 作业 > 视频）——同一门课里
//     「作业-第1章」与「讨论-导论」共用 leaf_type=6，只看数字会误判；
//  3. 名称无命中时退回 leaf_type 映射表。
func classify(title string, leafType int64) domain.TaskType {
	if leafType == LeafTypeDiscussGroup {
		return domain.TaskDiscuss
	}

	t := strings.ToLower(title)

	switch {
	case containsAny(t, examKeywords):
		return domain.TaskExam
	case containsAny(t, discussKeywords):
		return domain.TaskDiscuss
	case containsAny(t, homeworkKeywords):
		return domain.TaskHomework
	case containsAny(t, videoKeywords):
		return domain.TaskVideo
	}

	// 名称无命中时退回 leaf_type。
	if mapped, ok := leafTypeMap[leafType]; ok {
		return mapped
	}
	return domain.TaskOther
}

// containsAny 判断 s 是否包含任一关键词。
func containsAny(s string, keywords []string) bool {
	for _, k := range keywords {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

// mapLeafType 仅按类型数字映射（保留给测试与 probe 对照）。
func mapLeafType(lt int64) domain.TaskType {
	if t, ok := leafTypeMap[lt]; ok {
		return t
	}
	return domain.TaskOther
}

// routeKind 是网页端路由种类，与业务类型（domain.TaskType）解耦。
//
// 业务类型用于展示与提醒，路由用于打开页面，两者关注点不同。
// 反例：「P1.3 电路元件」按关键词是作业，但它是课件（leaf_type=7），
// 必须走 /video，走 quiz 报"没有权限"；
// 「课堂测试1」业务上是考试，却挂在老版 /v2/web/exam 下。
// 早期让业务类型直接决定路由，导致每修好一类就崩另一类。
type routeKind int

const (
	// routeChapter 是 ai-workspace 章节内路由（video/forum/quiz/node）。
	routeChapter routeKind = iota
	// routeExamLegacy 是老版考试页 /v2/web/exam/{cid}/{coursewareID}。
	routeExamLegacy
	// routeQuizSummary 是老版习题成绩页 /v2/web/quizSummary/{cid}/{id}。
	routeQuizSummary
)

// taskTypeFromLabel 把服务端的内容标签翻译成业务类型。
//
// 依据 /leaf_info 的 content_info.score_evaluation.name，实测取值：
// 视频 / 图文 / 讨论 / 作业 / 考试 / 课堂 / 雨课堂课件。
//
// 这是**最可靠的类型来源**：leaf_type 在同一门课内都会漂移，标题关键词
// 又会被"小组作业"这类名字骗到。
func taskTypeFromLabel(label string) domain.TaskType {
	switch strings.TrimSpace(label) {
	case "讨论":
		return domain.TaskDiscuss
	case "考试":
		return domain.TaskExam
	case "作业":
		return domain.TaskHomework
	case "视频", "图文", "课堂", "雨课堂课件":
		// 内容阅读器类：没有"提交"概念，等价于原先的 TaskVideo 语义。
		return domain.TaskVideo
	}
	return domain.TaskOther
}

// taskTypeFromInfo 综合服务端标签与计分字段判定业务类型。
//
// 「雨课堂课件」这个标签不能单独决定类型。leaf_type=7 的 21 个"课件"里，
// 既有纯阅读材料，也有 is_assessed=true、score_deadline 有值、占成绩 5%
// 的计分任务（「作业1单元」「期中2026」）。一律当 TaskVideo 会让这部分
// 任务不参与截止提醒。
//
// 判据用 is_assessed && score_deadline > 0，实测分布：
//
//	leaf_type  该组合是否出现   说明
//	5/7/9      是              作业/计分课件/考试
//	0/3/8      从不出现        纯视频/图文/课堂录像，无提交概念
//
// 这个组合比标签名更准，是"是否计分任务"的可靠判据。
func taskTypeFromInfo(d LeafInfoData) domain.TaskType {
	label := d.ContentLabel()
	assessedWithDeadline := d.IsAssessed.Bool() && d.ScoreDeadline.Unix() > 0

	switch strings.TrimSpace(label) {
	case "雨课堂课件":
		if assessedWithDeadline {
			return domain.TaskHomework
		}
		return domain.TaskVideo
	}
	return taskTypeFromLabel(label)
}

// chapterSegmentByContent 返回 ai-workspace 章节路由的路径段。
//
// 优先用服务端标签（content_info.score_evaluation.name），它比 leaf_type
// 可靠：同一门课内 leaf_type 会漂移——「讨论-导论」是 6、
// 「课前讨论：古典概型」是 0、「小组作业」是 4，而服务端标签始终准确。
// leafType 只在拿不到详情时兜底。
//
// 实测对照（浏览器逐条打开验证）：
//
//	服务端标签                路由    备注
//	讨论                      forum   需补 node_id=chapter_id
//	作业 / 考试               quiz    可被学习日志升级为老版页面
//	视频/图文/课堂/雨课堂课件   video   走 quiz 报"没有权限"
func chapterSegmentByContent(label string, leafType int64) string {
	switch strings.TrimSpace(label) {
	case "讨论":
		return "forum"
	case "作业", "考试":
		return "quiz"
	case "视频", "图文", "课堂", "雨课堂课件":
		// 这四类共用同一个内容阅读器，只有 video 路由能打开。
		return "video"
	}
	return chapterSegment(leafType)
}

// chapterSegment 按 leaf_type 推断路由段（拿不到服务端标签时的兜底）。
func chapterSegment(leafType int64) string {
	switch leafType {
	case 0, 3, 7, 8:
		return "video"
	case LeafTypeDiscussGroup:
		return "forum"
	case 5, 6, 9:
		return "quiz"
	default:
		return "node"
	}
}

// candidateLeaf 是遍历章节树时收集到的"可能是任务"的节点。
type candidateLeaf struct {
	course    string
	classroom string
	// leafID 是章节树里的节点 ID（id 字段），网页端路由与详情接口都用它。
	leafID string
	// leafInfoID 是 leafinfo_id 字段，仅用于排查，不参与任何请求。
	leafInfoID string
	// examID 是考试类叶子的 exam.exam_id；缺它就生成不了考试详情链接。
	examID string
	// chapterID 是该叶子所属章的 id（章节树字段 chapter_id）。
	//
	// ai-workspace 的 forum 路由除了路径段外还要求查询参数 node_id，
	// 而这个 node_id 取的是 chapter_id，不是叶子自己的 node_id 字段
	// （用后者会打开空白页）。
	chapterID string
	title     string
	leafType  int64
	deadline  int64
	startAt   int64
	status    domain.TaskStatus
}

// collectLeaves 深度遍历章节树，收集所有叶子。
//
// 结构（三层）：
//
//	course_chapter[]            ← 章
//	  └── section_leaf_list[]   ← 可能是"节"，也可能直接是叶子
//	        ├── leaf_list[]     ← "节"下面的真实叶子（视频/图文）
//	        └── (leaf_type!=0)  ← 自己就是叶子（作业/考试/讨论）
//
// section_leaf_list 里的视频类条目往往是"节"（leaf_type=0、无 leafinfo_id），
// 必须下钻 leaf_list 才能拿到真实叶子，否则生成的视频链接打不开。
// 同时兼容两种历史变体：叶子字段直接挂在章节上、章节嵌套 children[]。
func collectLeaves(chapters []ChapterNode, course, classroom string) []candidateLeaf {
	var out []candidateLeaf

	var walk func(ns []ChapterNode)
	walk = func(ns []ChapterNode) {
		for _, n := range ns {
			for _, leaf := range n.SectionLeafList {
				out = append(out, expandLeafNode(leaf, course, classroom)...)
			}

			// 章节本身就是一个叶子。
			if n.LeafType.Int64() != 0 || n.ScoreDeadline.Unix() != 0 || n.EndTime.Unix() != 0 {
				if leafID := firstNonEmpty(n.LeafID.String(), n.ID.String()); leafID != "" {
					deadline := n.ScoreDeadline.Unix()
					if deadline == 0 {
						deadline = n.EndTime.Unix()
					}
					out = append(out, candidateLeaf{
						course:     course,
						classroom:  classroom,
						leafID:     leafID,
						leafInfoID: n.LeafInfoID.String(),
						examID:     examIDOf(n.Exam),
						chapterID:  firstNonEmpty(n.ChapterID.String(), n.ID.String()),
						title:      n.Name.String(),
						leafType:   n.LeafType.Int64(),
						deadline:   deadline,
						startAt:    n.StartTime.Unix(),
						status:     parseStatus(n.Status.String()),
					})
				}
			}

			if len(n.Children) > 0 {
				walk(n.Children)
			}
		}
	}
	walk(chapters)
	return out
}

// expandLeafNode 把一个 section_leaf_list 条目展开成候选任务。
//
//   - 有 leaf_list → 它是"节"，下钻取真实叶子，节本身不是任务
//   - 无 leaf_list 且 leaf_type != 0 → 它自己就是叶子
//   - 无 leaf_list 且 leaf_type == 0 且无 leafinfo_id → 纯容器，跳过
func expandLeafNode(leaf LeafNode, course, classroom string) []candidateLeaf {
	if len(leaf.LeafList) > 0 {
		out := make([]candidateLeaf, 0, len(leaf.LeafList))
		for _, sub := range leaf.LeafList {
			// 视频叶子 leaf_type 可能是 0，但一定有 leafinfo_id。
			if sub.LeafInfoID.String() == "" && sub.LeafType.Int64() == 0 {
				continue
			}
			if c, ok := leafToCandidate(sub, course, classroom); ok {
				out = append(out, c)
			}
		}
		return out
	}

	if leaf.LeafType.Int64() == 0 && leaf.LeafInfoID.String() == "" &&
		leaf.ScoreDeadline.Unix() == 0 && leaf.EndTime.Unix() == 0 {
		return nil
	}

	if c, ok := leafToCandidate(leaf, course, classroom); ok {
		return []candidateLeaf{c}
	}
	return nil
}

// leafToCandidate 把 LeafNode 转成候选任务。
//
// 详情接口 /leaf_info/{cid}/{id}/ 的路径参数要的是章节树节点 id，
// 不是 leafinfo_id——用后者请求会返回 404000 "Objects does not exist."。
func leafToCandidate(leaf LeafNode, course, classroom string) (candidateLeaf, bool) {
	leafID := firstNonEmpty(leaf.ID.String(), leaf.LeafID.String())
	if leafID == "" {
		return candidateLeaf{}, false
	}

	deadline := leaf.ScoreDeadline.Unix()
	if deadline == 0 {
		deadline = leaf.EndTime.Unix()
	}

	return candidateLeaf{
		course:     course,
		classroom:  classroom,
		leafID:     leafID,
		leafInfoID: leaf.LeafInfoID.String(),
		examID:     examIDOf(leaf.Exam),
		chapterID:  leaf.ChapterID.String(),
		title:      leaf.Name.String(),
		leafType:   leaf.LeafType.Int64(),
		deadline:   deadline,
		startAt:    leaf.StartTime.Unix(),
		status:     parseStatus(leaf.Status.String()),
	}, true
}

// examIDOf 取考试叶子上的 exam_id；nil 安全。
func examIDOf(m *ExamMeta) string {
	if m == nil {
		return ""
	}
	return m.ExamID.String()
}

// collectFlatLeaves 处理平铺形式的叶子列表（data.leafinfo）。
func collectFlatLeaves(leaves []LeafNode, course, classroom string) []candidateLeaf {
	out := make([]candidateLeaf, 0, len(leaves))
	for _, l := range leaves {
		if c, ok := leafToCandidate(l, course, classroom); ok {
			out = append(out, c)
		}
	}
	return out
}

// parseStatus 把接口的状态字符串归一化。
func parseStatus(s string) domain.TaskStatus {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "done", "finished", "finish", "completed", "1", "true":
		return domain.StatusDone
	case "pending", "todo", "unfinished", "0", "false":
		return domain.StatusPending
	default:
		return domain.StatusUnknown
	}
}

// statusFromLeafInfo 从详情响应推断完成状态。
//
// 详情接口不提供完成状态：真实响应里没有 is_finish / status / score_status，
// force_reason 表达的是"还能不能提交"而非"做没做完"。完成状态一律由
// 学习日志的 status 字段提供（见 learnLogStatus），此处恒返回 Unknown。
func statusFromLeafInfo(d LeafInfoData) domain.TaskStatus {
	if d.IsDeleted.Bool() {
		return domain.StatusUnknown
	}
	return domain.StatusUnknown
}

// toTask 把候选叶子转成 domain.Task。
func toTask(c candidateLeaf, baseURL string) domain.Task {
	title := strings.TrimSpace(c.title)
	typ := classify(title, c.leafType)

	deadline := timeOrZero(c.deadline)
	// 视频/课件的 score_deadline 是整门课程的结束时间，不是任务截止，
	// 写入 Deadline 会让 UI 显示"剩余 85 天"这类误导信息。
	if !typ.HasDeadline() {
		deadline = time.Time{}
	}

	return domain.Task{
		ID:       taskID(c.classroom, c.leafID),
		Type:     typ,
		Title:    title,
		Course:   c.course,
		Deadline: deadline,
		StartAt:  timeOrZero(c.startAt),
		Status:   c.status,
		URL:      leafURL(baseURL, c),
		Source:   fmt.Sprintf("chapter:leaf_type=%d", c.leafType),
	}
}

// leafURL 为一个候选叶子生成直达链接。
//
// 章节树自带 exam_id 的试卷叶子走老版考试页，其余走由 leaf_type 决定的
// 章节内路由。学习日志的 courseware_id 在 fetchCourse 里另行覆盖。
func leafURL(baseURL string, c candidateLeaf) string {
	return leafURLWithLabel(baseURL, c, "")
}

// leafURLWithLabel 在已知服务端标签时生成链接（标签优先于 leaf_type）。
func leafURLWithLabel(baseURL string, c candidateLeaf, contentLabel string) string {
	segment := chapterSegmentByContent(contentLabel, c.leafType)
	if c.examID != "" && isPaperLeaf(c.leafType) {
		return taskURLByKind(baseURL, c.classroom, c.leafID, c.chapterID,
			c.examID, segment, routeExamLegacy)
	}
	return taskURLByKind(baseURL, c.classroom, c.leafID, c.chapterID,
		"", segment, routeChapter)
}

// webTaskURL 按业务类型生成链接（兼容旧调用点与测试）。
//
// 新代码应使用 taskURLByKind：业务类型与路由是两件事。
// 这个入口保留给 examToTask 与只需要"类型 → 链接"的简单场景。
func webTaskURL(baseURL, classroomID, leafID string, typ domain.TaskType, examID, chapterID string) string {
	if classroomID == "" || baseURL == "" {
		return baseURL + webHomeURL
	}
	if leafID != "" {
		switch typ {
		case domain.TaskHomework:
			// 能匹配到 courseware_id 时调用方会用 quizSummary 覆盖，
			// 那里才是查看成绩的正确落点。
			return baseURL + fmt.Sprintf(webQuizURLFormat, classroomID, leafID) + webChapterQuery
		case domain.TaskExam:
			// ai-workspace 的 quiz 路由只能定位到章节位置，打不开试卷本身。
			if examID != "" {
				return baseURL + fmt.Sprintf(webExamURLFormat, classroomID, examID)
			}
			return baseURL + fmt.Sprintf(webQuizURLFormat, classroomID, leafID) + webChapterQuery
		case domain.TaskDiscuss:
			return chapterURL(baseURL, classroomID, leafID, "forum",
				map[string]string{"node_id": chapterID})
		case domain.TaskVideo:
			return baseURL + fmt.Sprintf(webVideoURLFormat, classroomID, leafID) + webChapterQuery
		default:
			return chapterURL(baseURL, classroomID, leafID, "node", nil)
		}
	}
	// 没有叶子 ID 但知道 exam_id（来自 /v/exam/cover 的路径）。
	if typ == domain.TaskExam && examID != "" {
		return baseURL + fmt.Sprintf(webExamURLFormat, classroomID, examID)
	}
	return baseURL + webHomeURL
}

// chapterURL 拼 ai-workspace 章节内路由。
//
// segment 是内容实际挂载的组件（video/forum/quiz/node），不是业务类型。
// extra 里值为空串的键会被跳过。
func chapterURL(baseURL, classroomID, leafID, segment string, extra map[string]string) string {
	if classroomID == "" || baseURL == "" {
		return baseURL + webHomeURL
	}
	if leafID == "" || segment == "" {
		return baseURL + webHomeURL
	}
	u := baseURL + "/ai-workspace/lms-graph/" + classroomID + "/" + segment + "/" + leafID + webChapterQuery
	// 固定顺序输出，避免 map 遍历顺序导致 URL 抖动（影响去重键与测试）。
	for _, k := range []string{"node_id"} {
		if v := extra[k]; v != "" {
			u += "&" + k + "=" + v
		}
	}
	return u
}

// taskURLByKind 按路由种类生成链接，与业务类型无关。
//
// 路由由 leaf_type 与数据可用性决定；业务类型只影响"要不要提醒"，
// 不影响"点开能不能看到内容"。
//
//   - kind         路由种类（章节 / 老版考试页 / 老版成绩页）
//   - segment      章节路由的组件名（video/forum/quiz/node）
//   - coursewareID 学习日志给出的试卷 ID，仅老版路由使用
func taskURLByKind(baseURL, classroomID, leafID, chapterID, coursewareID, segment string, kind routeKind) string {
	if baseURL == "" || classroomID == "" {
		return baseURL + webHomeURL
	}
	switch kind {
	case routeExamLegacy:
		if coursewareID == "" {
			break
		}
		return baseURL + fmt.Sprintf(webExamURLFormat, classroomID, coursewareID)
	case routeQuizSummary:
		if coursewareID == "" {
			break
		}
		return baseURL + fmt.Sprintf(webQuizSummaryURLFormat, classroomID, coursewareID)
	}
	// routeChapter（或老版路由缺 ID 时的兜底）
	var extra map[string]string
	if segment == "forum" {
		extra = map[string]string{"node_id": chapterID}
	}
	return chapterURL(baseURL, classroomID, leafID, segment, extra)
}

// isPureContentLeaf 判断该叶子是否为纯内容类（无需拉详情）。
//
// 判据用 leaf_type 而非 classify：后者按标题关键词猜，会把「作业1单元」
// 「期中2026」这类计分课件（leaf_type=7）判错。
//
// 依据 is_assessed × score_deadline 的实测分布：
//
//	leaf_type 3 图文      从不出现计分组合  → 纯内容
//	leaf_type 8 课堂录像  从不出现          → 纯内容
//	leaf_type 0 视频      部分出现          → 不跳过（有 28 个计分任务）
//	leaf_type 7 课件      全部出现          → 不跳过
//	leaf_type 4/5/6/9     部分出现          → 不跳过
func isPureContentLeaf(leaf candidateLeaf) bool {
	switch leaf.leafType {
	case 3, 8:
		return true
	}
	return false
}

// isPaperLeaf 判断该 leaf_type 是否属于试卷类（作业/考试）。
//
// 只有试卷类才会出现在学习日志里、并有 courseware_id 指向老版页面。
// 课件(3/7)、视频(0/8)、讨论(4) 的 courseware_id 不是试卷 ID，
// 拿来拼链接会得到打不开的地址。
func isPaperLeaf(leafType int64) bool {
	switch leafType {
	case 5, 6, 9:
		return true
	}
	return false
}

// needsCoursewareID 判断这门课是否值得拉学习日志。
//
// 只看 leaf_type：按标题关键词判定会把课件也算进来，白花请求。
func needsCoursewareID(leaves []candidateLeaf) bool {
	for _, leaf := range leaves {
		if isPaperLeaf(leaf.leafType) {
			return true
		}
	}
	return false
}

// coursewareIndex 是"标题 → 试卷 ID"的索引，由学习日志构建。
//
// 按标题而非 ID 匹配：章节树给的是 node id / leafinfo_id，学习日志给的是
// courseware_id，两者之间没有可 join 的字段。标题是唯一稳定的公共信息。
// 匹配不上只会让链接退回 ai-workspace 路由，不会报错，因此这个弱关联
// 是可以接受的折中。
type coursewareIndex struct {
	// byTitle 的键是归一化后的标题。
	byTitle map[string]coursewareRef
}

// coursewareRef 是一条学习日志里与试卷相关的定位信息。
type coursewareRef struct {
	coursewareID string
	kind         int64 // LearnLogQuiz / LearnLogExam
	// status 是学习日志的完成状态编码，语义见 learnLogStatus。
	status int64
	// statusKnown 表示响应里确实带了 status 字段。
	statusKnown bool
}

// learnLogStatus 把学习日志的 status 编码翻译为完成状态。
//
// 依据 65 条作业/考试日志与 score 字段的逐条比对：
//
//	status=3  → score 有值（含 0/4 这种低分），48 条 → 已完成
//	status=1  → score 为空，5 条                   → 未完成
//	status=0  → score 为空，12 条                  → 未完成
//
// 0 与 1 可能有"未开始/已开始未提交"的区别，但都意味着还没交，
// 对提醒而言同属未完成。
func learnLogStatus(s int64, known bool) domain.TaskStatus {
	if !known {
		return domain.StatusUnknown
	}
	if s == 3 {
		return domain.StatusDone
	}
	return domain.StatusPending
}

// normalizeTitle 归一化标题用于匹配。
//
// 只做去首尾空白与合并连续空白，不删标点：章节树与学习日志的标题
// 连全角引号都一致，过度归一化反而会把不同任务判成同一个。
func normalizeTitle(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

// buildCoursewareIndex 从学习日志构建索引。
//
// 只收 type=4（习题）与 type=5（考试）：其它类型（公告、视频）的
// courseware_id 不是试卷 ID。
func (c *Client) buildCoursewareIndex(ctx context.Context, classroomID string, stats *FetchStats) *coursewareIndex {
	logs, cached, err := c.learnLogsCached(ctx, classroomID)
	if err != nil {
		// 学习日志拿不到不影响任务本身，只影响链接精度。
		c.log.Debug("学习日志拉取失败，链接退回章节路由",
			"classroom", classroomID, "err", err)
		return nil
	}
	if cached {
		stats.LogsCached++
	} else {
		stats.LogsFetched++
	}

	idx := &coursewareIndex{byTitle: make(map[string]coursewareRef, len(logs))}
	for _, a := range logs {
		id := strings.TrimSpace(a.CoursewareID.String())
		if id == "" {
			continue
		}
		kind := a.Type.Int64()
		if kind != LearnLogQuiz && kind != LearnLogExam {
			continue
		}
		title := normalizeTitle(a.Title.String())
		if title == "" {
			continue
		}
		hasStatus := a.Status.Defined()
		idx.byTitle[title] = coursewareRef{
			coursewareID: id,
			kind:         kind,
			status:       a.Status.Int64(),
			statusKnown:  hasStatus,
		}
	}
	if len(idx.byTitle) == 0 {
		return nil
	}
	return idx
}

// lookup 按标题查找试卷信息。
func (idx *coursewareIndex) lookup(title string) (coursewareRef, bool) {
	if idx == nil {
		return coursewareRef{}, false
	}
	ref, ok := idx.byTitle[normalizeTitle(title)]
	return ref, ok
}

// webURL 生成该试卷的网页端直达链接。
//
// 路由由学习日志的 type 决定：
//
//	type=5 考试  → /v2/web/exam/{cid}/{courseware_id}
//	type=4 习题  → /v2/web/quizSummary/{cid}/{courseware_id}
//
// 两者都是老版 UI，但这是唯一能真正打开对应试卷的地址——
// ai-workspace 的 quiz 路由只能定位到章节位置，不带试卷内容。
func (r coursewareRef) webURL(baseURL, classroomID string) string {
	if baseURL == "" || classroomID == "" || r.coursewareID == "" {
		return ""
	}
	switch r.kind {
	case LearnLogExam:
		return baseURL + fmt.Sprintf(webExamURLFormat, classroomID, r.coursewareID)
	case LearnLogQuiz:
		return baseURL + fmt.Sprintf(webQuizSummaryURLFormat, classroomID, r.coursewareID)
	}
	return ""
}

// taskID 构造稳定唯一键：classroom + leaf 即可跨轮次稳定。
func taskID(classroom, leafID string) string {
	return "ykt:" + classroom + ":" + leafID
}

// timeOrZero 把 Unix 秒转为 time.Time；0 表示无时间。
func timeOrZero(sec int64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// applyLeafInfo 用详情接口的结果补全任务信息。
//
// 详情通常比章节树更准确，因此以详情为准，但不会清空已有的有效值。
func applyLeafInfo(t domain.Task, d LeafInfoData) domain.Task {
	// 真实响应里标题字段名是 name；Title 作为兼容保留。
	if title := strings.TrimSpace(d.Title.String()); title != "" {
		t.Title = title
	}
	// 服务端标签优先：score_evaluation.name 是权威类型，比标题关键词可靠
	// （「小组作业」标签是"讨论"、「作业1单元」标签是"雨课堂课件"）。
	// 计分课件再由 is_assessed + score_deadline 提升为作业。
	if label := d.ContentLabel(); label != "" {
		t.Type = taskTypeFromInfo(d)
	} else if t.Title != "" {
		var lt int64
		if _, err := fmt.Sscanf(t.Source, "chapter:leaf_type=%d", &lt); err == nil {
			t.Type = classify(t.Title, lt)
		}
	}

	// 严格只用 score_deadline。class_end_time 是整门课的结束时间，
	// 拿它兜底会让已截止的作业显示成"还有 84 天"，掩盖真正的逾期。
	if t.Type.HasDeadline() {
		if sec := d.ScoreDeadline.Unix(); sec != 0 {
			t.Deadline = timeOrZero(sec)
		}
	}

	if sec := d.ClassStartTime.Unix(); sec != 0 && t.StartAt.IsZero() {
		t.StartAt = timeOrZero(sec)
	}
	if st := statusFromLeafInfo(d); st != domain.StatusUnknown {
		t.Status = st
	}
	return t
}

// examToTask 把考试封面转成 domain.Task。
func examToTask(course string, classroom string, d ExamCoverData, baseURL string) domain.Task {
	deadline := d.Deadline.Unix()
	if deadline == 0 {
		deadline = d.ScoreDeadline.Unix()
	}
	status := domain.StatusUnknown
	switch strings.ToLower(strings.TrimSpace(d.Result.Status.String())) {
	case "submitted", "finished", "done", "1":
		status = domain.StatusDone
	case "":
		status = domain.StatusPending
	}
	title := d.TitleText()
	if title == "" {
		title = "考试"
	}
	return domain.Task{
		ID:       taskID(classroom, "exam:"+d.ExamID.String()),
		Type:     domain.TaskExam,
		Title:    title,
		Course:   course,
		Deadline: timeOrZero(deadline),
		StartAt:  timeOrZero(d.StartTime.Unix()),
		Status:   status,
		URL:      webTaskURL(baseURL, classroom, "", domain.TaskExam, d.ExamID.String(), ""),
		Source:   "exam:cover",
	}
}
