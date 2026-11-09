# 架构说明

## 设计目标与约束

1. **只读**：任何情况下都不提交作业、不答题、不交卷。这条约束体现在代码结构里——`internal/ykt` 不提供写方法，`endpoints.go` 只包含 GET 接口。
2. **低占用**：单二进制、纯 Go、无 cgo、无前端构建步骤。发布构建约 13 MB，常驻 RSS 25–35 MB。
3. **可维护**：接口细节只在一个包里，变更时改动面最小。
4. **可测试**：领域层零 I/O，全部是纯函数。

---

## 分层

```
┌──────────────────────────────────────────────────────────┐
│ cmd/rainclass-reminder      仅解析 os.Args，转交 cli            │
└────────────────────────┬─────────────────────────────────┘
                         ▼
┌──────────────────────────────────────────────────────────┐
│ internal/cli          子命令 + 依赖装配（App）            │
│                       唯一知道"怎么把各层拼起来"的地方    │
└───┬──────────┬──────────┬──────────┬──────────┬──────────┘
    ▼          ▼          ▼          ▼          ▼
┌────────┐┌────────┐┌──────────┐┌────────┐┌─────────────┐
│scheduler││ webui  ││  auth    ││ store  ││  notify     │
│ 编排    ││ 面板   ││ 登录态   ││ 持久化 ││ 推送通道    │
└───┬────┘└───┬────┘└────┬─────┘└───┬────┘└──────┬──────┘
    │         │          │          │            │
    ├─────────┴──────────┴──────────┴────────────┘
    ▼
┌──────────────────────────────────────────────────────────┐
│ internal/ykt          雨课堂 API 客户端（只读）           │
│                       唯一知道端点与字段含义的层          │
└────────────────────────┬─────────────────────────────────┘
                         ▼
┌──────────────────────────────────────────────────────────┐
│ internal/domain       纯领域：Task / Milestone / Policy   │
│                       零 import，零 I/O，100% 可单测      │
└──────────────────────────────────────────────────────────┘
```

**依赖规则**：箭头只能向下。`domain` 不 import 任何 internal 包；
`ykt` 的内部依赖只有 `domain`；调度核心通过 `Fetcher` / `Notifier` / `Repository` 工作，
`scheduler/source.go` 中的 `YktSource` 适配器负责连接具体的 `ykt.Client`。

---

## 数据流（一轮）

```
scheduler.RunCycle
   │
   ├─1─ Fetcher.FetchTasks(ctx)
   │      └─ ykt.Client.FetchAll
   │            ├─ GET /v2/api/web/courses/list      → []CourseItem
   │            ├─ GET /mooc-api/.../course/chapter  → ChapterData（带 TTL 缓存）
   │            ├─ GET /mooc-api/.../leaf_info/{cid}/{leaf}  → LeafInfoData（仅近 7 天）
   │            └─ mapper.ToTask → []domain.Task
   │
   ├─2─ store.SaveTasks(tasks)          // 快照，供面板与失败兜底
   │
   ├─3─ store.PushedKeys()              // 已发送集合
   │
   ├─4─ domain.Decide(now, tasks, sent, policy)
   │      ├─ MilestonesFor(task) → 截止前 N 窗口 + 过期 + 开课
   │      ├─ 过滤：已发送 / 未到点 / 超出 lookahead
   │      ├─ 静默时段顺延（非紧急项）
   │      └─ Coalesce：每任务只留最紧急一条 + 单轮上限
   │
   ├─5─ 对每条提醒：
   │      ├─ store.Claim(key)   // 原子占位，防重复推送
   │      ├─ notify.RenderReminder → Message
   │      ├─ Registry.Send → 飞书卡片
   │      └─ store.Record(status=sent|failed)
   │
   └─6─ 对抑制项：store.Record(status=suppressed)
          ↑ 关键：不做这步会导致被抑制的里程碑每轮重复出现
```

---

## 关键设计决策

### 0. 首次运行静默历史提醒（bootstrap）

新装用户第一次启动时数据库为空，历史任务的过期里程碑会被判定为
"该发未发"，一次性补发上百条"已截止"通知。实测真实账号触发 620 条。

修复：首次运行（`PushedKeys` 为空）时，把 `fireAt` 早于 `now - 24h`
的里程碑直接记为 `suppressed`，保留最近 24 小时内的（刚截止的仍应提醒）。

### 1. 去重键包含截止时间

```go
key = taskID | kind | offsetMinutes | deadline.Unix()
```

好处：老师改期后 `deadline` 变化 → 键不同 → 自动重新计时并提醒。
这是从 `asklight/zhihuishu-notifier` 的成熟做法沿用。

### 2. Claim / Record / Release 三态

| 状态 | 含义 | 是否计入已发送 |
|---|---|---|
| `claimed` | 已占位、正在发送 | ❌ |
| `sent` | 发送成功 | ✅ |
| `failed` | 发送失败，允许重试 | ❌ |
| `suppressed` | 被合并/配额压制，视为已处理 | ✅ |

`Claim` 用一条原子 UPSERT 完成"检查 + 占位"：

```sql
INSERT INTO pushes (...) VALUES (?, 'claimed', ...)
ON CONFLICT(key) DO UPDATE SET status='claimed'
WHERE pushes.status='failed'
```

这样既防止多实例重复推送，又允许失败重试。

### 3. 抑制项也要记账

最初实现只对"实际发送"的项记账，导致被 coalesce 压制的窗口在下一轮
重新成为候选，形成"每次轮询都提醒一次"的缺陷。测试 `TestRunCycleSendsOnce`
暴露了这个问题。现在抑制项也写入 `suppressed` 状态。

### 4. MaxLookahead 放在 Decide 而不是 MilestonesFor

```go
// 正确：相对当前时刻判断
if ms.FireAt.After(now.Add(cfg.MaxLookahead)) { continue }
if ms.FireAt.Before(now.Add(-cfg.MaxLookahead)) { continue }
```

最初写在 `MilestonesFor` 里比较 `Deadline - FireAt`（即窗口长度），
逻辑上恒为真，等于没做限制。测试 `TestDecideMaxLookahead` 抓到了。

### 5. 宽容解析（flex.go）

雨课堂同一字段在不同课程/版本下类型不稳定（`123` / `"123"` / `123.0`）。
`FlexInt` / `FlexString` / `FlexTime` 解析失败时退化为零值而非报错，
保证"一个坏字段不影响整个任务列表"。

### 6. 请求量控制

- 章节树按 `classroom_id` 缓存 `chapter_ttl`（默认 30 分钟）
- 只对 `detail_lookahead_days` 天内到期的叶子拉详情
- 并发信号量限制为 `poll.concurrency`（默认 4）
- 轮询叠加 ±50% 抖动，非活跃时段拉长到 `idle_interval`

实测（33 门课、363 个叶子）：每轮 33 次章节请求 + 279 次详情请求 = 312 个请求，
每 10 分钟一轮 → 平均 31 个请求/分钟。首次之后章节树命中缓存，
详情请求数会显著下降（只有新变更的叶子才需要重拉）。

### 7. 业务类型与网页路由分别判定

`leaf_type` 数字在同一门课内语义不一致（`6` 同时用于"作业-第1章"和"讨论-导论"）。
有详情时，`taskTypeFromInfo()` 优先使用服务端标签；没有详情时，`classify()`
按标题关键词判定，`leaf_type` 兜底。「雨课堂课件」保持 `video` 类型，
计分与考核截止不足以证明它是需要提交的作业。

网页路由由 `chapterSegmentByContent()` 独立判定：课件走 `video` 阅读器，
不从业务类型倒推路由。学习日志中的 `courseware_id` 仅用于试卷类路由升级。

同理，`leaf_type=0/8`（视频）虽有 `score_deadline`，但那是**课程结束时间**，
因此 `TaskType.HasDeadline()` 对视频返回 false，避免期末批量误报。

---

## 扩展点

面板的跨课堂合并仅发生在 `webui/assets/task-view.mjs`。相同课程名、标题、类型、
截止与开始时间的不同课堂记录折叠展示；同一课堂有重名项或身份缺失时不合并。
这是展示上的匹配，并不证明来源内容相同。课堂筛选在分组前生效，状态筛选在分组后生效，
组内保留各课堂的完成状态、分数与链接。API、持久化、提醒键和发送流程仍逐课堂处理。

`learn-log` 的 `score` / `total_score` 通过 `OptFloat` 保留缺失、零分和小数，
经 `coursewareRef`、`Task`、SQLite nullable REAL 列到面板 JSON（缺失为 null）。
分数不参与完成状态判定，也不改变原有 6 小时学习日志缓存；刷新面板不保证立即更新成绩。
旧库打开时事务性补列，已有任务和推送记录保留。

| 想加什么 | 改哪里 |
|---|---|
| 新推送通道（邮件/QQ/TG） | 实现 `notify.Notifier`，在 `cli/app.go` 的 factories 注册 |
| 新任务类型识别 | `internal/ykt/mapper.go` 的 `leafTypeMap` 加一行 |
| 新 API 端点 | `internal/ykt/endpoints.go` + 一个 `api.go` 方法 |
| 新提醒规则 | `internal/domain/milestone.go` 的 `MilestonesFor` |
| 换存储后端 | 实现 `store.Repository` 接口 |

## 被动观察与平台演进

`ykt.Observer` 是可选输出端口，正常请求结束时输出类别、状态与固定结果分类。
`BasicInfo` / `Courses` 解析业务包后再输出认证结果，避免把 HTTP 200 当成登录成功。
CLI 负责将端口装配到 `internal/observe`，后者只保存白名单 JSONL；`ykt` 不依赖记录器、文件路径或报表工具。
观察不开启时没有记录输出，不额外发请求，不改变缓存或轮询策略。跨重启的 24h 请求窗口与持续认证证据见 [OBSERVATION.md](OBSERVATION.md)。

Windows GUI 与 Android 的目标边界、取舍及必须先验证的依赖见 [ADR 0001](adr/0001-platform-clients.md)。

Android 已接入 `internal/application`：一次刷新继续装配既有 `scheduler.YktSource`、策略和去重存储；
平台通过 Vault / Sink 提供加密凭据和通知，不复制类型映射或提醒算法。`bindings/mobile` 是独立模块，
只导出绑定支持的基础类型、接口和版本化 JSON；根目录 `go test ./...` 不会覆盖它，需要 Android CI 单独构建。
`notify` 的纯文本处理与桌面通道分开，Android 构建排除 beeep，不引入 CLI / WebUI。

Android Worker 和前台界面共享一个串行应用引擎；后台由系统安排，每次只执行一轮。
Android `data/RefreshQueue` 串行化引擎调用。前台和 Worker 重叠时，等待方复用等待期间成功完成的
同步快照；失败不算完成，重新认证或退出使排在会话变更后的旧请求取消。未重叠的手动刷新仍正常执行。
`ScreenDataSource` 是界面读取快照、刷新和退出的窄接口，便于用合成数据验证异步状态；不复制 Go 抓取规则。
凭据与账号标识保存在 Keystore 支持的加密存储，任务和去重记录留在本机 SQLite。
跨存储的账号切换无法原子提交，重开时比较加密会话中的账号与库内标识，不一致则清空旧缓存；
验证失败或凭据保存失败不清理旧账号数据。注销先取消请求并清凭据，未登录快照不导出残留缓存。
构建、平台模块和验收范围见 [ANDROID.md](ANDROID.md)。

Android 的 `ui/TaskPresentation.kt` 只负责快照的排序、状态分组、搜索和计数，不修改业务类型、
URL、数据库顺序或提醒策略。`RainScreen` 编排页面；任务卡片、筛选、主题与设置分文件维护。
界面每 30 秒更新一次时间，进入后台停止计时，回到前台立即重新计算；不增加网络请求。

2026-11-09 复核未发现 `domain` 的平台依赖或 Android 依赖 CLI / WebUI。
仍有四项需独立处理，不能把边界清晰等同于业务无缺陷：

- `ykt/mapper.go` 的数字 `leaf_type=7` 兜底为作业，与文件注释和课件证据不一致；详情不可用时可能误提醒。
- `TaskClass` 有开课规则，但当前生产映射没有生成该类型。服务端“课堂”当前按视频处理，须先辨明录播与真实课堂来源，不能直接改标签映射。
- `application/mobile.go` 保存新会话后，SQLite 重置失败时当前进程仍可能持有旧会话；重开修复不能替代当前进程的失败恢复。
- `ykt.Client.SetSession` 未清学习日志缓存；Android 当前新建 client 避开此路径，但复用 client 的调用方仍有旧缓存风险。

另有旧版 URL 生成分支重复；当前考试回退路径未发现错误，后续收敛必须继续保留业务类型与路由的独立判据。

基本信息的当前 `id` 与旧版 `user_id` 在 `ykt.BasicInfoData` 解码时统一，应用层不判断接口版本。
移动认证校验归一后的非零身份，再保存加密会话；Cookie 存在或 HTTP 200 不替代身份校验。
Android 默认扫码由 `application.Login` 封装 `auth.QRLogin`。Go 持有 OAuth 二维码轮询和五分钟取消上下文，
绑定只暴露开始、等待与取消。`LoginViewModel` 在普通切后台和页面重建时保留任务；退出、换码或释放
ViewModel 时取消，旧协程返回前再检查取消状态。系统回收进程后重新获取二维码，不持久化 OAuth 状态。
方案依据与边界见 [ADR 0002](adr/0002-android-native-qr-login.md)。

OAuth 成功后，Cookie 仍交给 `Mobile.Authenticate` 做非零身份校验，再写入 Keystore 支持的加密会话存储；
Cookie 存在或 HTTP 200 不替代身份校验。认证成功通过 Activity Result 通知首页，先显示缓存并立即同步课程；
普通启动仅恢复本地快照。原官方 WebView 保留为备用入口，其域名限制和 Cookie 检查继续适用，业务端点仍只使用 GET。

历史记录：alpha.8 和 alpha.9 调试包曾提供 OAuth 确认链接与公众号场景链接两个分享入口，
2026-11-09 用户真机测试均未成功，两个入口已被否定。alpha.10 已移除公众号场景实现、绑定导出和剪贴板入口；
`API_NOTES.md` 仅把该实验的两个认证端点留作研究记录，应用当前不再调用。账号登录入口继续暂缓；
微信授权研究尚未解决单台手机上的会话交接。

移动引擎独立持有设备时区，刷新前由 Android 更新；tzdata 仅随绑定模块打包，不改变桌面全局时区。
Worker 先创建带取消上下文的刷新对象，再登记执行，取消按请求传递到抓取层，覆盖执行前停止的竞争。
共享调度器遇到明确认证失败时暂停缓存提醒并发送去重告警，只有完整抓取成功才清除告警标记。
