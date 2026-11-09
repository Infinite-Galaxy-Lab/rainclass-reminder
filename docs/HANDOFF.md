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
| 分支 | `codex/android-release`；Android 与本轮认证恢复改动在此分支，尚未合并 main |
| 测试 | 197 个 Go 测试函数、6 个面板纯逻辑测试、9 个 Python 工具测试；Android 11 项模拟器设备测试通过 |
| CI | 3 平台 Go 测试、4 平台构建、cgo 断言、面板及 Python 工具测试；远端结果以 Actions 为准 |
| 构建 | 六平台交叉编译，13 MB，零 cgo |
| 注释密度 | 15%（只保留非显然的 WHY） |

```bash
go test ./... && go vet ./... && gofmt -l ./cmd ./internal
node --test internal/webui/task-view.test.mjs
python -m unittest discover -s tools -p 'test_*.py' -v
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

### P2 工具与平台规划

前述功能已于 2026-11-08 推送 main（`f4b7d51`），该提交的 Actions 通过。
本轮随后新增可选的 `ykt.Observer`、本地白名单记录器、脱敏报告脚本与浏览器 CDP 监听脚本，
不新增业务请求。请求记录从首次实际启动起持续 24 小时，重启不重置；认证结果继续记录。
只将数据解释为本地导入后的可用性证据，不能声称已测得服务端真实寿命。

本机自启任务名 `YktReminder`，新启动入口在 `%LOCALAPPDATA%\RainClassReminder`。
安排在 **2026-11-09 00:00 +08:00 之后首次用户登录**延迟 30 秒启动；今天不重启当前服务。
沿用已有配置与数据目录，部署设置与旧任务备份留在私有目录。官方网页监听仍需用户在专用浏览器窗口登录，
不能标成已完成全天观察。步骤见 [OBSERVATION.md](OBSERVATION.md)。

Go 全量测试、vet、gofmt、6 项 Node、9 项 Python 通过；新增回归完成实际反向验证。
Windows 启动器用合成进程验证时间门槛、中文路径、环境变量与退出码；
浏览器监听用本地合成 TLS 服务器验证 CDP、Set-Cookie 存在标记、脱敏与退出时保留浏览器。
没有以真实账号流量冒充这些工具验证。

上一轮 Windows GUI 建议 Wails 薄壳，Android 建议 Kotlin / Compose + 小型 Go AAR。
Android 本轮已按此方向生成 demo，进度与限制见下；Windows GUI 安装包尚未制作。
见 [ADR 0001](adr/0001-platform-clients.md)、[ROADMAP.md](ROADMAP.md) 与 [OPERATIONS.md](OPERATIONS.md)。

## 未做待办（按优先级）

### Android 内测与观察恢复 · 2026-11-09

分支 `codex/android-release`。已建立 Kotlin / Compose 工程、小型 gomobile AAR 绑定和
`internal/application`，复用既有抓取、SQLite、策略与去重。已生成 Android `0.1.0-alpha.2`
签名 demo APK，产物在忽略目录 `dist/android/`，不创建正式发行标签。项目图标使用第二版。
模块、构建与签名要求见 [ANDROID.md](ANDROID.md)。

交付包 `dist/android/rainclass-reminder-0.1.0-alpha.2.apk`（35,270,675 字节）已通过签名与
16 KiB zipalign 检查，并在 MuMu Android 15 从 alpha.1 覆盖升级、启动成功。SHA-256：
`88eb61a4ca2702c2d7c52fa56062435d545dbf2ab697face20859e26791e7621`。
alpha.1 仍单独保留在忽略目录。源码与 CI 对应本分支提交，不沿用 main 的旧 CI 结果。
该 APK 由干净提交 `194318b` 重新生成 AAR 后构建，构建信息保存在忽略目录。

已验证：arm64-v8a / x86_64 AAR、Go 核心与 SQLite 在 Android 编译运行、Keystore 密文与重置、
SQLite 重开和 nullable 成绩、唯一周期 Worker / 取消、通知拒绝和关闭渠道、320dp 界面与设置。
MuMu Android 15 共 10 项设备测试通过；今日新增 4 项 Kotlin 实现破坏均取得断言失败，恢复后全绿。
Go 今日新增 8 个测试函数；时区、DST、通知时间、请求取消、启动恢复与失效告警相关的 10 项
反向验证均通过。197 项 Go tests、vet、gofmt、原有 Node / Python 测试通过。
远端核心 CI `42547208400`（`194318b`）与 Android CI `42547781128`（`a10597a`）均通过。
后者覆盖 Linux 上双 ABI AAR、debug APK 构建与 lint；10 项设备测试来自本机模拟器，非远端真机验收。

扫码页空白已在当前手机比例模拟器复现：WebView 未指定布局尺寸，实际页面高度为零；官方
固定宽度页面又使二维码超出窄屏。现显式填满可用区域，只对官方登录页修正 viewport 并开放缩放，
增加重载与加载失败提示。debug 检查确认二维码完整落在可见范围；release 关闭 WebView 调试。
这不等于真实手机扫码登录通过，仍待用户验证。

设备时区现由 Android 传入，每个引擎独立配置，每次刷新更新，不修改全局 time.Local。
移动绑定内置 tzdata；静默顺延使用日历日期，通知时间按同一时区渲染。Worker 取消改为持有具体
刷新请求，停止早于请求执行也会保留取消状态，不误伤其他请求。

Cookie 过期提醒已存在：服务端明确认证失败时，调用既有 `AlertAuthExpired` 持久化去重并
发一次重登通知；网络错误不算过期。Android 还需系统通知权限和实际执行刷新。设备上的真实
过期通知全流程仍待验证，不能保证后台被限制时即时提醒。

Windows 启动认证失败时现在保留面板与服务，便于重登；`--once` 仍失败退出。运行中认证失败
暂停缓存任务提醒，成功完成抓取才清除告警去重，网络故障不清标记。

今日重启后原自启失败。09:01:55 首次建立观察目录时已明确认证失败；缺少最近成功样本，
不能从本地导入至首次失败的约 21 小时间隔推算真实寿命。任务无法访问原 Local 部署目录，
现迁至 `%APPDATA%\ykt-reminder\runtime`，沿用原配置与数据并复制已有观察窗口。
09:37:28 从计划任务实际启动成功，任务为 Running、面板 HTTP 200、观察文件更新。
下次登录触发仍需重启验收；官方网页监听尚未运行。旧设置与任务备份仍在原私有 rollback 目录。

### 登录页与全天观察续办 · 2026-11-09

用户 11:01:54 在桌面重登成功。新一轮项目请求观察已从 **11-09 11:06:36 +08:00** 开始，
截至 **11-10 11:06:36 +08:00**；同一计划任务运行，面板 HTTP 200，记录已出现认证成功。
旧观察目录保留；新目录从 runtime/settings.json 的 observation_dir 读取。认证结果在请求窗口结束后继续记录。
官方网页侧仍无记录，须用户手动打开专用窗口并登录；不能将项目请求或暂未失效解释为已测得完整寿命。

用户实机反馈 alpha.2 已显示二维码，但过小且禁止截图。alpha.3 移除登录窗口的 FLAG_SECURE，
只对官方登录页做设备宽度排版，二维码框最大 240px，保留切换登录方式、协议和滚动，加入同机操作说明。
11 项模拟器设备测试通过；恢复截图禁令、恢复桌面宽度、恢复遮挡二维码的切换角标三项反向验证
均为断言失败，恢复后全绿。320dp 两项登录测试通过，模拟器分辨率已恢复。
新布局在匿名官方页检查，不含真实账号登录验收。旧 alpha.1 / alpha.2 APK 保留。

交付 `dist/android/rainclass-reminder-0.1.0-alpha.3.apk`，35,271,791 字节，SHA-256：
`55f64354257952fb52795ce4954a26cd84ab22f68a6237dd9ad92e5541516879`。
沿用原内测签名、versionCode 3，验签与 16 KiB zipalign 通过，MuMu 从 alpha.2 覆盖升级并启动。
Go 核心未变化，复用 alpha.2 干净提交生成的 AAR；源码提交 `4ca6faf` 的工作区干净重建与交付包
哈希一致，构建信息和独立备份已保存。Android CI `42556426484` 对该提交的 Linux AAR、debug APK
和 lint 全部通过，设备回归来自本机模拟器。文档跟进提交不改变 APK 对应源码。

下轮按以下顺序继续：

1. **持续桌面观察**：已取得成功样本，11-10 11:06:36 后查看 24h 项目请求报告；认证寿命继续观察，下次登录确认自启，网页侧另开专用观察。
2. **Android 真实登录**：用户在 alpha.3 验证手机二维码、截图后微信相册识别或另一台设备扫码，Go 校验同域会话，登录后刷新。
   未使用真实账号登录本轮 Android demo。
3. **Cookie 过期端到端**：真实失效、只提醒一次、通知被拒绝后可重试、重登恢复；不把断网判过期。
4. **后台设备验收**：物理手机、重启、进程回收、Doze / 厂商省电、断网恢复。
   Worker stop 取消竞争已通过设备测试及反向验证；硬件与系统调度仍待实测，不承诺精确提醒。
5. **发行与管理**：API 26 / arm64 设备、16 KiB 页设备、版本回滚、
   密钥跨机加密备份恢复，再决定正式分发；当前本地密钥备份与证书核对通过，但仅 DPAPI 密码副本不能跨机恢复。

模拟器证据、截图、工具链与签名均在仓库外。SDK 工具下载期间发现 Windows 系统代理未被
Java / curl 自动继承；本机构建缓存已配置，不能把个人代理路径/设置提交到项目。

### P2 · 需要时间

- **登录态持续观察** — 计划任务与项目观察已恢复，用户已重登取得成功样本；当前窗口 11-09 11:06:36 至 11-10 11:06:36，Set-Cookie 存在标记不证明续期成功。
- **官方网页 Network** — 脱敏监听脚本已提供，待专用浏览器登录并观察；项目侧采集不能替代网页侧证据。

### P4 · 功能增强

- **Windows GUI / Android 发行版** — Android alpha.3 放大二维码并允许截图，待真机、真实登录与后台验收；Windows Wails 壳尚未做。iOS 后续评估。
- 自有图标已替换 README 占位图并接入 Android demo，PNG/ICO 见 `docs/BRANDING.md`。Windows 发行包图标与面板接入尚未完成。设计提示词仅在仓库外私有保存。

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
python -m unittest discover -s tools -p 'test_*.py' -v
```

下轮先查看本次观察报告、验证 Android alpha.3 真实登录，再推进后台设备验收；时区已修复并回归。
