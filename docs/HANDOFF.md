# 交接说明

给接手这个项目的 agent。读完这一页就能开始工作，细节都在 `docs/`。

---

## 项目是什么

**RainClass Reminder**（`https://github.com/Infinite-Galaxy-Lab/rainclass-reminder`）

长江雨课堂的截止提醒助手。一个常驻后台的**只读**守护进程，定时拉取作业/考试/讨论/课堂任务，在截止前通过飞书、桌面通知、通用 Webhook 或 SMTP 邮件推送。

**只读是第一设计约束**：全部请求为 GET，不提交、不答题、不交卷。这条约束落在代码结构里——`internal/ykt/endpoints.go` 只含 GET 路径，审阅这一个文件即可确认。

未来会发行移动端 App，所以数据层与表现层应当保持可复用。

---

## 当前状态

| | |
|---|---|
| 版本 | 0.7.0 |
| 分支 | 发布工作基于远端 `main`；最新提交与验证见 Git 历史及 CI |
| 测试 | 172 个 Go 测试函数、6 个面板纯逻辑测试，本地全绿 |
| CI | 3 平台 Go 测试、4 平台构建、cgo 断言及面板纯逻辑测试；远端结果以 Actions 为准 |
| 构建 | 六平台交叉编译，13 MB，零 cgo |
| 注释密度 | 15%（只保留非显然的 WHY） |

```bash
go test ./... && go vet ./... && gofmt -l ./cmd ./internal
node --test internal/webui/task-view.test.mjs
bash deploy/build.sh          # 六平台产出到 dist/
```

---

## 架构

依赖方向严格单向，`domain` 不 import 任何 internal 包。

```
cmd/rainclass-reminder   入口
internal/cli             子命令与依赖装配
internal/scheduler       编排：抓取 → 决策 → 发送 → 记账
internal/domain          纯领域（Task/Milestone/Policy），零 I/O
internal/ykt             唯一知道雨课堂 API 的层
internal/notify          推送（Notifier 接口 + 飞书/桌面/Webhook/邮件）
internal/store           持久化（Repository 接口 + SQLite）
internal/auth            登录态（cookie 导入 / 微信扫码）
internal/netguard        SSRF 地址校验
internal/webui           本地面板（embed 静态资源）
```

**动手前必读**：`docs/ARCHITECTURE.md`（分层与数据流）、`docs/API_NOTES.md`（接口真实结构）、`docs/EVOLUTION.md`（踩坑归档）。

---

## 三条必须知道的硬约束

### 1. 业务类型 ≠ 网页路由

这是这个项目最容易踩的坑，历史上修了四轮才理清。

- **业务类型**（`domain.TaskType`）：影响展示标签与是否按截止时间提醒
- **路由**（`routeKind` + segment）：决定链接指向哪个页面

两者必须解耦。反例：「P1.3 电路元件」曾按标题关键词猜成作业，但服务端标为课件，必须走 `/video`，走 `/quiz` 报"没有权限"。曾经的实现让业务类型直接决定路由，结果每修好一类就崩另一类。

类型的**权威来源**是 `/leaf_info` 的 `content_info.score_evaluation.name`（服务端标签），不是 `leaf_type`——后者在同一门课内都会漂移（讨论可能是 6、0 或 4）。

### 2. ID 有三种，不能混用

| 用途 | 字段 |
|---|---|
| 网页路由路径参数、详情接口路径参数 | 章节树节点 `id` |
| 讨论路由的 `node_id` 查询参数 | 叶子的 `chapter_id`（不是叶子自己的 `node_id`） |
| 老版考试/成绩页路径参数 | 学习日志的 `courseware_id` |

传错任何一个都会打开「此内容已被老师删除」或空白页。

### 3. 宽容解析不能静默吞掉关键字段

`flex.go` 里的 `FlexInt`/`FlexString`/`FlexTime` 解析失败退化为零值。这个策略保证单个坏字段不影响整体，但也意味着**解析器写错了不会报错，只会表现为"字段凭空消失"**。

真实案例：`/leaf_info` 返回的毫秒时间戳带小数（`1778428740000.0`），早期只用 `ParseInt`，解析失败退化为 0，导致 33 个计分课件不参与提醒。

改 `flex.go` 时，新增的格式必须有对应测试。

---

## 本轮完成（2026-11-08）

### P1 · 计分课件判据回退

通过真实登录态查看两个课堂中的「作业1单元」「指针(3)」，共四页，均打开 `/video` 阅读器，展示学习完成度与考核截止提示，未见答题或提交入口。四项均已过期，不能据此断言所有有效期内页面都没有提交入口。

已移除 `taskTypeFromInfo` 中 `is_assessed && score_deadline > 0` 将课件提升为作业的规则，恢复按服务端标签归类；课件不参与截止提醒，网页链接仍由独立路由判据决定。若后续出现有效期内的相反证据，应补充具体服务端标签与页面行为，不恢复泛化猜测。

### P3 · 仅合并面板展示

用户已确认。不同课堂的课程名、标题、类型、截止与开始时间一致时折叠为一项；仅归一首尾与连续空白，不模糊匹配。同一课堂有多个重名项或缺少可靠课堂 ID、课程名、标题时不合并。

展开后保留每个课堂的状态、分数和链接，不计算合并成绩。选定课堂后只显示该课堂；筛选未完成任务时保留同组已完成课堂的详情。角标按展示项计数，列表说明同时给出课堂记录数。API、存储与提醒仍按课堂独立处理。

### P4-1 · 面板分数

`learn-log` 的 `score` / `total_score` 保留缺失、零分和小数，经 DTO、领域模型、SQLite 到面板。缺失为 null，不显示成 0；完成状态仍以服务端 status 判定。旧库打开时事务性补列，已有任务和推送记录保留。

沿用 6 小时学习日志缓存，没有增加请求；面板刷新不保证即时取得新成绩。

### 素材与隐私

README 使用新版图标 `docs/assets/icon-v2.png`，第一版 PNG/ICO 保留。个人头像两版均在仓库外交付。设计提示词、参考原图与凭据不纳入仓库。隐私检查与历史残留见 `docs/PRIVACY_REVIEW.md`。

### 验证记录

接手时先跑通 163 个 Go 测试函数的基线。本轮全量 `go test ./...`、`go vet ./...`、
`gofmt -l ./cmd ./internal`、6 项 Node 测试与 `git diff --check` 均通过。
新增回归做了 16 轮反向验证，均检出断言或运行失败，非编译失败，并恢复绿测。
合成数据的桌面与 390px 窄屏浏览器检查覆盖分组、成绩、课堂筛选、键盘展开和横向溢出。
真实账号检查只用于 P1，不发布页面截图或账号数据；未替换运行中的服务。

## 未做待办（按优先级）

### P2 · 需要时间

- **测量 `sessionid` 真实寿命** — 已确认服务端从不下发 `sessionid` 的 Set-Cookie（见 `docs/EVOLUTION.md` §4.7.1），不会自动续期。缺的是"能撑多久"这个数，需要持续运行到 `ErrAuthExpired`。
- **DevTools 抓一天 Network** — 唯一能回答"官方客户端为什么更持久"的办法。

### P4 · 功能增强

- **移动端 App 发行版** — Android/iOS。桌面端逻辑可复用，数据层（`domain` + `ykt` + `store`）应保持无 UI 依赖。
- 自有图标已替换 README 占位图，PNG/ICO 见 `docs/BRANDING.md`。发行包图标与面板接入尚未完成。设计提示词仅在仓库外私有保存。

### P5 · 低优先级

- 持续确认不同课件形态，尤其有效期内是否有提交入口；数字 `leaf_type=7` 不作为业务类型的权威依据
- 解析静默失败告警（非空输入解析成 0 时告警）
- 浏览器扩展 / PWA 面板
- 历史推送分页
- 历史提交隐私清理：需授权重写提交历史并协调强制推送，见 `docs/PRIVACY_REVIEW.md`

---

## 协作约定

**注释**：只写"不查代码就看不出理由"的内容。不要写"这个 bug 在 v0.6.x 被发现"这类流水账，不要复述代码字面意思。

**文档**：不写口水话、不写 emoji 堆砌、不用"在当今时代"这类开场。表格与代码块优先。

**测试**：新增回归测试必须反向验证——故意改坏实现，确认测试真的会红，再改回来。编译失败不算验证。

**凭据**：任何密钥、token、cookie 都不进源码、示例配置、测试。全部走环境变量。仓库是公开的。

**前端**：由用户本人做，不委派给子代理。

---

## 环境

| | |
|---|---|
| Go | `D:\toolchain\go\bin`（不在 PATH，需 `export PATH="/d/toolchain/go/bin:$PATH"`） |
| `-race` | 本机不可用（`0xc0000139`），是环境问题不是代码问题 |
| GitHub | `Infinite-Galaxy-Lab`，凭据从 git credential helper 取 |
| 推送 | `git push` 常被连接重置，`api.github.com` 通常可达；长文件改动可走 contents API |

---

## 立刻可以开始的检查

```bash
export PATH="/d/toolchain/go/bin:$PATH"
cd D:/code/长江雨课堂通知助手
go test ./...          # 应全绿
go vet ./...           # 应无输出
gofmt -l ./cmd ./internal  # 应无输出
node --test internal/webui/task-view.test.mjs
```

下一步优先积累 P2 的登录态寿命证据，再推进移动端与发行素材接入。
