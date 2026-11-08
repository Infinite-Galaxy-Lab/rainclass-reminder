package ykt

// 本文件集中所有雨课堂接口路径，接口变更时只需修改这里。
//
// 仅包含只读 GET 接口。任何提交/答题/交卷类路径都不得出现在此文件中，
// 这既是安全约束，也让代码审阅可以一眼确认"只读"。

// 相对路径常量（相对于 Client.baseURL）。
const (
	pathUserBasicInfo = "/api/v3/user/basic-info"

	// pathCourseList 课程列表；identity=2 表示学生视角。
	pathCourseList = "/v2/api/web/courses/list"

	// pathChapter 章节树，叶节点含 score_deadline / end_time。
	pathChapter = "/mooc-api/v1/lms/learn/course/chapter"

	// pathLeafInfo 叶子详情。路径参数为 cid 与章节树节点 id，
	// 并且必须带 classroom-id 请求头，否则返回
	// {"success":false,"error_code":40000,"msg":"CLASSROOM ID IS REQUIRED"}。
	pathLeafInfo = "/mooc-api/v1/lms/learn/leaf_info/%s/%s/"

	// pathExamCover 考试封面，含 deadline / start_time。
	pathExamCover = "/v/exam/cover"

	// pathLearnLogs 学习日志，是唯一能拿到试卷真实 ID 的接口。
	//
	//	GET /v2/api/web/logs/learn/{classroom_id}?actype=-1&page=0&offset=20&sort=-1
	//	→ data.activities[].courseware_id
	//
	// 章节树里没有这个 ID（exam.exam_id 全站为空）。网页端就是靠它拼
	// /v2/web/exam/{cid}/{id} 与 /v2/web/quizSummary/{cid}/{id}。
	// actype=-1 表示全部；actype=100 返回空列表。
	pathLearnLogs = "/v2/api/web/logs/learn/%s"

	// pathCourseMetaUserInfo 触发 csrftoken 下发的轻量接口（登录流程用）。
	pathCourseMetaUserInfo = "/v/course_meta/user_info"
)

// 网页端路由，仅用于生成直达链接，不参与抓取。
//
// ai-workspace 的路由表（取自其前端打包产物）：
//
//	base: "/ai-workspace", history 模式
//	/lms-graph/:classroom_id  children:
//	    quiz/:leaf_id      exercise/:leaf_id   forum/:leaf_id
//	    video/:leaf_id     audio/:leaf_id      graph/:leaf_id
//	    node/:node_id      self-test/:node_id  pdf/:leaf_id
//
// 路径里的 :leaf_id 要传章节树节点 id，不是 leafinfo_id——
// 用后者会打开「此内容已被老师删除」。
const (
	// webGraphURLFormat 是 ai-workspace 详情路由的公共前缀。
	webGraphURLFormat = "/ai-workspace/lms-graph/%s/"

	// webExamURLFormat 考试详情页（老版 UI，带试卷封面与"继续答题"）。
	//
	//	/v2/web/exam/24984528/2051258
	//	其中 2051258 是 exam_id，等于学习日志里的 courseware_id（type=5）。
	webExamURLFormat = "/v2/web/exam/%s/%s"

	// webQuizSummaryURLFormat 习题成绩/回顾页（老版 UI）。
	//
	//	/v2/web/quizSummary/25570085/1745475
	//	其中 1745475 是 quiz_id，等于学习日志里的 courseware_id（type=4）。
	//	展示得分、错题与逐题答案。
	webQuizSummaryURLFormat = "/v2/web/quizSummary/%s/%s"

	webExerciseURLFormat = webGraphURLFormat + "exercise/%s"
	webQuizURLFormat     = webGraphURLFormat + "quiz/%s"
	webForumURLFormat    = webGraphURLFormat + "forum/%s"
	webVideoURLFormat    = webGraphURLFormat + "video/%s"

	// webNodeURLFormat 通用节点页，类型未知时的兜底。
	webNodeURLFormat = webGraphURLFormat + "node/%s"

	// webChapterQuery 是详情页需要的查询参数。不带时部分页面取不到
	// 章节上下文，会渲染成空白。
	webChapterQuery = "?is_chapter=1"

	// webHomeURL 首页，也是缺参数时的兜底。
	webHomeURL = "/v2/web/index"
)
