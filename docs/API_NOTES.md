# 雨课堂 API 笔记

> 本文记录本工具实际使用的接口。**只包含只读 GET 接口**。
>
> **重要**：以下结构基于 2026-11 真实账号验证（33 门课程、363 个叶子）。
> 公开资料里的字段名（`data.chapters`、`leaf_id`）与本实例实际不符，
> 请以本文为准；接口变更时用 `ykt-reminder probe` 重新取证。

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

响应 `data`：`user_id`、`nickname`、`name`、`school`

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
        "classroom_id": 26804510,
        "name": "2026“AI+信息素养”大赛",
        "short_name": null,
        "students_count": 82,
        "teacher": {"user_id": 1470464, "name": "周建芳"},
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
    "course_id": 3427311,
    "course_name": "2026“AI+信息素养”大赛",
    "course_chapter": [
      {
        "id": 6516468,
        "name": "未分类教学活动",
        "order": -1,
        "section_leaf_list": [
          {
            "id": 49195196,
            "leafinfo_id": 49197417,
            "leaf_type": 5,
            "name": "模拟题一",
            "start_time": 1790070110000,
            "end_time": 0,
            "score_deadline": 1821628799000,
            "chapter_id": 6516468,
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

**`id` vs `leafinfo_id` 必须区分**：`id` 用于唯一标识任务，`leafinfo_id` 才是详情接口需要传的参数。混用会导致详情 404。

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

### 关键：名称关键词优先于 leaf_type

实测同一门课里 `leaf_type=6` 既可能是「作业-第1章」也可能是「讨论-导论」，
而 `leaf_type=7` 有「期中2026」这种考试。**只看数字必然误判**，
因此 `classify()` 按标题关键词优先判定：

| 关键词 | 判定 |
|---|---|
| 考试、期中、期末、补考 | `exam` |
| 讨论、研讨 | `discuss` |
| 作业、习题、练习、测验、测试、模拟题、单元 | `homework` |
| 视频、课件、课程简介 | `video` |
| 无关键词 | 回退 `leaf_type` |

### 关键：视频类的 `score_deadline` 是课程结束时间

`leaf_type=0/8` 的 167 个视频节点都带 `score_deadline`，但值都是
**整门课程的结束时间**（如 `2027-01-31 23:59`）。若按它提醒，
期末会一次性涌出上百条无意义通知。

**因此 `TaskType.HasDeadline()` 对 `video` 返回 false** —— 视频不参与截止提醒。

---

## 4. 叶子详情（补全标题与完成状态）

```
GET /mooc-api/v1/lms/learn/leaf_info/{cid}/{leafinfo_id}/
Header: xtbz: ykt, classroom-id: {cid}
```

响应 `data`：

| 字段 | 说明 |
|---|---|
| `title` / `name` | 标题（可能比章节树更完整） |
| `score_deadline` | 截止时间 |
| `publish_time` | 发布时间 |
| `class_start_time` / `class_end_time` | 课堂起止 |
| `is_finish` | `1` = 已完成 |
| `status` / `score_status` | 状态字符串 |

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
         → window.wx_code='...'
6. GET  /api/v3/user/login/wechat-web-callback?path=...&code=...&state=...
         → 302 = 成功，cookie 出现 sessionid
```

实现要点（都是踩过的坑）：
- 必须**关闭自动重定向**，否则拿不到 302 判定
- 全程复用同一 cookie jar，否则 `csrftoken` 丢失
- 二维码返回 **JPEG**，需 `image.Decode` 自动识别格式（不能假定 PNG）
- 轮询期间 `wx_code` 为空表示未扫码，`40x` 表示已扫码未确认

---

## 接口变更排查流程

1. `ykt-reminder probe --out probe_out` → 生成脱敏快照
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
- 视频/课件虽有 `score_deadline` 但是课程结束时间，**刻意不提醒**
- 16/33 门课程 `course_chapter` 为空（历史课/未发布），属正常
- 考试域 cookie 与主站不互通，本工具不进入考试域（提醒不需要）
