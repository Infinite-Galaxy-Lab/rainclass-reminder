package ykt

// 所有接口路径集中在这里，接口变更只改本文件。
//
// 只允许只读 GET 接口——审阅本文件即可确认"只读"约束没被破坏。

const (
	pathUserBasicInfo = "/api/v3/user/basic-info"

	// identity=2 表示学生视角。
	pathCourseList = "/v2/api/web/courses/list"

	pathChapter = "/mooc-api/v1/lms/learn/course/chapter"

	// 路径参数为 cid 与章节树节点 id（不是 leafinfo_id），且必须带
	// classroom-id 请求头，否则返回 error_code 40000。
	pathLeafInfo = "/mooc-api/v1/lms/learn/leaf_info/%s/%s/"

	pathExamCover = "/v/exam/cover"

	// 学习日志是唯一能拿到试卷真实 ID 的接口（章节树里 exam.exam_id 全空）。
	// actype=-1 表示全部；actype=100 返回空列表。
	pathLearnLogs = "/v2/api/web/logs/learn/%s"

	// 触发 csrftoken 下发的轻量接口，登录流程用。
	pathCourseMetaUserInfo = "/v/course_meta/user_info"
)

// 网页端路由，仅用于生成直达链接，不参与抓取。
//
// 章节路由形如 /ai-workspace/lms-graph/{classroom_id}/{quiz|video|forum}/{leaf_id}，
// 其中 :leaf_id 要传章节树节点 id——用 leafinfo_id 会打开「此内容已被老师删除」。
const (
	webGraphURLFormat = "/ai-workspace/lms-graph/%s/"

	// 考试与习题只有老版页面能打开试卷本身，第二个参数是 courseware_id。
	webExamURLFormat        = "/v2/web/exam/%s/%s"
	webQuizSummaryURLFormat = "/v2/web/quizSummary/%s/%s"

	webExerciseURLFormat = webGraphURLFormat + "exercise/%s"
	webQuizURLFormat     = webGraphURLFormat + "quiz/%s"
	webForumURLFormat    = webGraphURLFormat + "forum/%s"
	webVideoURLFormat    = webGraphURLFormat + "video/%s"
	webNodeURLFormat     = webGraphURLFormat + "node/%s"

	// 不带这个参数时部分页面取不到章节上下文，会渲染成空白。
	webChapterQuery = "?is_chapter=1"

	webHomeURL = "/v2/web/index"
)
