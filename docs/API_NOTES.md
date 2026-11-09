# 雨课堂 API 笔记

> 业务抓取仅使用只读 GET 接口；微信登录流程单列于第 6 节，不涉及作业提交。
>
> **重要**：以下结构基于 2026-11 真实账号验证（33 门课程、363 个叶子）。
> 公开资料里的字段名（`data.chapters`、`leaf_id`）与本实例实际不符，
> 请以本文为准；接口变更时用 `rainclass-reminder probe` 重新取证。
> 文中的姓名与关联 ID 使用示例值，不能用于访问真实课程。

---

## 通用约定

| 项目 | 值 |
|---|---|
| 站点 | `https://changjiang.yuketang.cn` |
| 登录凭据 | Cookie：`sessionid` + `csrftoken`（各 32 位） |
| 必需请求头 | `xtbz: ykt`、`Referer: <base>/v2/web/index`、常规浏览器 UA |
| 响应信封 | `{"success": true, "data": {...}}`；部分接口无 `errcode` |
| 时间字段 | **Unix 毫秒**（`1790070110000`）；`0` 表示无时间 |
| 登录失效信号 | HTTP 302/401/403，或返回 HTML 登录页 |

---

## 1. 校验登录态

```
GET /api/v3/user/basic-info
```

2026-11-09 在 Android 官方 WebView 扫码后确认当前响应形状如下；示例 ID 为合成值：

```json
{"code":0,"msg":"success","data":{"id":"101","nickname":"示例昵称","name":"示例姓名","school":"示例学校"}}
```

当前账号字段为 `data.id`，兼容旧版 `data.user_id`，两者均可为数字或字符串。
DTO 归一为 `BasicInfoData.UserID`；旧字段非空时优先，缺失或 null 时取 `id`。
移动认证仍要求身份非空且不为零，不能仅凭 HTTP 200 或 Cookie 存在认定登录成功。
此前只解析 `user_id` 会使已经完成官方扫码的有效会话被移动端拒绝。

用途：每轮开始前确认登录态；失效则发一次性告警。

---

## 2. 课程列表

```
GET /v2/api/web/courses/list?identity=2
```

`identity=2` = 学生视角。

**响应结构**（实测）：

```json
{
  "errcode": 0,
  "errmsg": "Success",
  "data": {
    "list": [
      {
        "classroom_id": 80000009,
        "name": "2026“AI+信息素养”大赛",
        "short_name": null,
        "students_count": 82,
        "teacher": {"user_id": 10001, "name": "示例教师"},
        "role": 5,
        "course": {"name": "..."}
      }
    ]
  }
}
```

关键点：
- 课程数组在 **`data.list`**（不是 `data.courses`）
- `classroom_id` 是**数字**，需转成字符串后作为 `cid` 使用
- 33 门课程里只有 17 门有内容，其余 `course_chapter` 为空数组（历史课/未发布）

---

## 3. 章节树（截止时间的主要来源）

```
GET /mooc-api/v1/lms/learn/course/chapter?cid={classroom_id}&classroom_id={classroom_id}
Header: xtbz: ykt
```

**响应结构**（实测，这是最容易搞错的地方）：

```json
{
  "data": {
    "course_id": 7000005,
    "course_name": "2026“AI+信息素养”大赛",
    "course_chapter": [
      {
        "id": 7000009,
        "name": "未分类教学活动",
        "order": -1,
        "section_leaf_list": [
          {
            "id": 80000025,
            "leafinfo_id": 80000026,
            "leaf_type": 5,
            "name": "模拟题一",
            "start_time": 1790070110000,
            "end_time": 0,
            "score_deadline": 1821628799000,
            "chapter_id": 7000009,
            "is_show": true,
            "is_locked": false,
            "is_score": true,
            "exam": {"online_proctor": 0, "is_offline": false}
          }
        ]
      }
    ]
  }
}
```

**与公开资料的差异**：

| 项目 | 公开资料 | 实际 |
|---|---|---|
| 根节点 | `data.chapters` | `data.course_chapter` |
| 叶子容器 | `children` | `section_leaf_list` |
| 叶子 ID | `leaf_id` | `id`（+ 独立的 `leafinfo_id`） |
| 课程名 | `data.course_info.name` | `data.course_name` |

**`id` vs `leafinfo_id` 必须区分**：网页路由和详情接口都传章节树节点 `id`。
`leafinfo_id` 仅保留用于排障，不能传给详情接口，否则会返回对象不存在。
章节树可能另有 `section_leaf_list[].leaf_list[]` 一层；非空容器应下钻收集实际叶子。

### leaf_type 实测分布（363 个叶子）

| 值 | 数量 | 实际含义 | 映射 |
|---|---|---|---|
| 0 | 33 | 视频章节 | `video` |
| 3 | 11 | 章节/讨论 | `discuss` |
| 4 | 1 | 小组作业 | `homework` |
| 5 | 59 | 模拟题/习题 | `homework` |
| 6 | 24 | 作业与讨论混杂 | `homework` |
| 7 | 33 | 预习/复习材料 | `homework` |
| 8 | **134** | 视频章节（最多） | `video` |
| 9 | 38 | 随堂测验/练习题 | `homework` |
| 无 | 30 | 视频小节 | `video` |

### 关键：服务端标签优先，名称与 leaf_type 用于兜底

实测同一门课里 `leaf_type=6` 既可能是「作业-第1章」也可能是「讨论-导论」，
而 `leaf_type=7` 有标题为「期中2026」的节点。**只看数字必然误判**，
有详情时，业务类型以 `taskTypeFromInfo()` 读取的服务端标签为准；
没有详情时，`classify()` 按标题关键词判定：

| 关键词 | 判定 |
|---|---|
| 考试、期中、期末、补考 | `exam` |
| 讨论、研讨 | `discuss` |
| 作业、习题、练习、测验、测试、模拟题、单元 | `homework` |
| 视频、课件、课程简介 | `video` |
| 无关键词 | 回退 `leaf_type` |

业务类型与网页路由独立判定。「雨课堂课件」按服务端标签归为 `video`，
不因 `is_assessed` 与 `score_deadline` 同时存在就提升为作业。
2026-11-08 检查两个课堂的四个已过期课件，均为学习完成度阅读器，未见提交入口，
因此回退此前缺少证据的提升规则。样本不覆盖有效期内的全部形态。

### 关键：视频类的 `score_deadline` 是课程结束时间

`leaf_type=0/8` 的 167 个视频节点都带 `score_deadline`，但值都是
**整门课程的结束时间**（如 `2027-01-31 23:59`）。若按它提醒，
期末会一次性涌出上百条无意义通知。

**因此 `TaskType.HasDeadline()` 对 `video` 返回 false** —— 视频不参与截止提醒。

---

## 4. 叶子详情（补全标题、截止时间与内容标签）

```
GET /mooc-api/v1/lms/learn/leaf_info/{cid}/{id}/
Header: xtbz: ykt, classroom-id: {cid}
```

响应 `data`：

| 字段 | 说明 |
|---|---|
| `name` | 标题（可能比章节树更完整） |
| `score_deadline` | 截止时间 |
| `publish_time` | 发布时间 |
| `class_start_time` / `class_end_time` | 课堂起止 |
| `is_locked` / `is_deleted` / `force_reason` | 可见性与访问限制 |
| `is_assessed` | 是否计分 |
| `content_info.score_evaluation.name` | 服务端内容标签，用于独立判定业务类型和路由 |

`score_deadline` 可能是带小数的毫秒时间戳（如 `1778428740000.0`），不能只用整数解析。
`class_end_time` 不作为作业截止时间的兜底。实测详情没有 `is_finish`、`status` 或 `score_status`；
完成状态来自学习日志。

### 学习日志（完成状态与试卷 ID）

```
GET /v2/api/web/logs/learn/{cid}?actype=-1&page=0&offset=20&sort=-1
```

`data.activities[]` 的 `status=3` 表示已完成，`0/1` 表示未完成；字段缺失与零值必须区分。
`type=4/5` 的 `courseware_id` 分别用于习题成绩页与考试页，按归一化标题关联章节任务。
它不是章节树节点 `id`，也不是 `leafinfo_id`。

`score` / `total_score` 可为数字、数字字符串或 null；零分与缺失不同，小数不截断。
非法或非有限数值按缺失处理。分数只展示，不参与完成状态判定。
沿用试卷日志索引的匹配范围与 6 小时缓存，未匹配到的章节任务不猜测成绩。

调用策略：只对 `detail_lookahead_days`（默认 7）天内到期的叶子调用。
实测 363 个叶子中拉取 279 个、跳过 84 个，0 错误。

---

## 5. 考试封面

```
GET /v/exam/cover?exam_id={exam_id}&classroom_id={cid}
Header: xtbz: ykt
```

响应 `data`：`deadline`、`start_time`、`limit`（时长分钟）、`result.status`

本工具**不**调用考试域 `changjiang-exam.yuketang.cn`，也不调用 `/v/exam/gen_token`。

---

## 6. 微信扫码登录（实测可用）

```
1. GET  /v/course_meta/user_info                      → 下发 csrftoken
2. POST /api/v3/user/login/wechat-auth-param          → {appId, state, redirectUri}
3. GET  https://open.weixin.qq.com/connect/qrconnect  → HTML 中提取 /connect/qrcode/{uid}
4. GET  https://open.weixin.qq.com/connect/qrcode/{uid} → **JPEG** 图片（不是 PNG！）
5. GET  https://lp.open.weixin.qq.com/connect/l/qrconnect?uuid={uid}
         → window.wx_errcode=状态; window.wx_code='确认码'
6. GET  /api/v3/user/login/wechat-web-callback?path=...&code=...&state=...
         → 302 = 成功，cookie 出现 sessionid
```

实现要点（都是踩过的坑）：
- 必须**关闭自动重定向**，否则拿不到 302 判定
- 全程复用同一 cookie jar，否则 `csrftoken` 丢失
- 二维码返回 **JPEG**，需 `image.Decode` 自动识别格式（不能假定 PNG）
- 状态在 `wx_errcode`，与 OAuth 确认码 `wx_code` 分开；空确认码不代表从未扫码
- 官方页面收到 `404`（已扫码）或 `403`（拒绝）后，下一次请求回传 `last`；收到 `408` 后不再带旧状态
- 2026-11-09 已补 `last` 回传及两种状态的合成回归，用户随后确认 MuMu 切后台扫码、返回首页成功；真机同手机相册识码被微信拒绝，是独立限制

Android alpha.10 继续使用上述原生 OAuth：二维码由应用展示，轮询由 Go 持有，取消贯穿等待；
扫码结果仍须通过 basic-info 的非零身份校验，再保存到加密会话，成功后立即同步课程快照。
官方网页保留为备用入口，业务 API 的只读 GET 边界保持不变。

公众号场景实验只保留以下两个认证端点作为历史研究记录，alpha.10 已移除其实现、绑定导出和
剪贴板分享入口，应用当前不调用：

| 认证端点 | 记录 |
|---|---|
| `GET /api/v3/user/login/pre-info` | 场景取码入口 |
| `POST /api/v3/user/login` | 场景等待入口 |

alpha.8 和 alpha.9 调试包曾分别提供 OAuth 确认链接和公众号场景链接的复制入口，尝试粘贴到微信；
2026-11-09 用户真机测试均未成功，两个分享入口已被否定。账号登录入口继续暂缓；
微信授权研究尚未解决单台手机上的会话交接。

---

## 接口变更排查流程

1. `rainclass-reminder probe --out probe_out` → 生成脱敏快照
2. 快照里看：课程数、每门课 `leaf_types` 分布、任务字段
3. 若 `leaf_types` 出现新值 → 更新 `leafTypeMap` 或 `classify` 关键词
4. 若结构变化 → 更新 `dto.go` 的 json tag
5. 若时间从毫秒变秒 → `FlexTime` 已按量级自动归一，无需改
6. 跑 `go test ./internal/ykt/` 确认端到端测试仍通过

辅助脚本：
- `tools/probe_raw.py` — 打印原始响应骨架（排障用）
- `tools/probe_types.py` — 统计 leaf_type 分布与样例标题

---

## 已知限制

- **讨论与课堂**没有独立的截止时间字段；课堂用 `start_time` 做开课提醒
- 视频与课件不参与截止提醒；计分和考核截止不能单独证明存在作业提交入口
- 16/33 门课程 `course_chapter` 为空（历史课/未发布），属正常
- 考试域 cookie 与主站不互通，本工具不进入考试域（提醒不需要）
