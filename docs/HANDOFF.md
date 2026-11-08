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
| 分支 | `main`，与远端同步 |
| 测试 | 163 个测试函数，全绿 |
| CI | GitHub Actions 通过（3 平台测试 + 4 平台构建 + cgo 断言） |
| 代码量 | 8,385 行 Go（非测试）+ 5,086 行测试 + 1,784 行前端 |
| 构建 | 六平台交叉编译，13 MB，零 cgo |
| 注释密度 | 15%（只保留非显然的 WHY） |

```bash
go test ./... && go vet ./... && gofmt -l ./cmd ./internal
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

两者必须解耦。反例：「P1.3 电路元件」按标题关键词是作业，但它是课件（`leaf_type=7`），必须走 `/video`，走 `/quiz` 报"没有权限"。曾经的实现让业务类型直接决定路由，结果每修好一类就崩另一类。

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

## 待办（按优先级）

### P1 · 需要真机确认

**计分课件是否真有提交入口** — 面板里的「作业1单元」「指针(3)」现在被判为作业（因为 `is_assessed && score_deadline > 0`）。判据在 `mapper.go` 的 `taskTypeFromInfo`。**如果它们其实只是阅读材料，这个判据要回退。** 这是唯一一处"改了判据但没有真机反证"的改动。

### P2 · 需要时间

- **测量 `sessionid` 真实寿命** — 已确认服务端从不下发 `sessionid` 的 Set-Cookie（见 `docs/EVOLUTION.md` §4.7.1），不会自动续期。缺的是"能撑多久"这个数，需要持续运行到 `ErrAuthExpired`。
- **DevTools 抓一天 Network** — 唯一能回答"官方客户端为什么更持久"的办法。

### P3 · 待产品决策

- **跨课堂同名任务合并** — 实测 21 组重复（如「作业1单元」同时存在于 23175789 与 23175790）。合并会让单课堂状态变糊，需要先定语义。

### P4 · 功能增强

- **面板显示分数** — `learn-log` 里有 `score` / `total_score`，可展示成「T1.1 5/5」，比现在只显示"已完成"有用。
- **移动端 App 发行版** — Android/iOS。桌面端逻辑可复用，数据层（`domain` + `ykt` + `store`）应保持无 UI 依赖。
- 图标替换 — 当前用官方图标占位，需生成自有设计（融合"书页 + 圆形进度 + 铃铛"）。

### P5 · 低优先级

- `leaf_type=7` 业务类型最终确认
- 解析静默失败告警（非空输入解析成 0 时告警）
- 浏览器扩展 / PWA 面板
- 历史推送分页

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
go test ./...          # 应全绿，163 个测试
go vet ./...           # 应无输出
gofmt -l ./cmd ./internal  # 应无输出
```

跑通了就可以从上面 P1 或 P4 里挑一件开始。
