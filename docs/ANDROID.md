# Android 内测版

Android 使用 Kotlin / Jetpack Compose 界面和 WorkManager，任务抓取、类型映射、缓存、提醒策略和去重复用 Go。Windows 守护进程与 Android 各自持有本机数据库，不同步凭据或推送记录。

## 模块边界

| 目录 | 职责 |
|---|---|
| `internal/application` | 登录校验、一次刷新、缓存查询、注销、取消；平台凭据与通知端口 |
| `bindings/mobile` | gomobile 支持的基础类型与接口，导出 JSON schema 1；独立 Go 模块 |
| `apps/android/app/.../data` | Keystore 加密凭据、JSON 读取、串行调用 Go |
| `apps/android/app/.../reminders` | 系统通知权限、通知渠道、唯一周期 Worker |
| `apps/android/app/.../ui` | 列表、搜索、状态筛选、成绩、设置；不推断雨课堂业务类型 |

依赖从 Android 经绑定进入应用用例，再调用现有 scheduler / ykt / store / domain。`notify/desktop.go` 用构建标签排除 Android；移动依赖闭包不能引入 CLI、WebUI 或 beeep。根模块仍兼容 Go 1.23，移动工具链独立升级。

Go 用例把业务请求限定为现有只读 GET。点击任务会交给外部浏览器；官方登录页面运行在受限 WebView，未注入 JavaScript 桥接。会话只传给服务端校验，原始错误、响应正文和用户标识不会成为界面错误文本。

## 能力与限制

当前为 `0.1.0-alpha.4` 内测包。MuMu Android 15 已由用户真实扫码登录，完成任务刷新，
结束进程再打开后登录与缓存保留；物理手机上的同版本登录和后台仍待验收。
设备时区已接入；不能将模拟器回归通过视为后台通知可靠性验收。

- 原生任务列表、手动刷新、搜索与状态筛选；成绩区分缺失、零分和小数。
- 缓存离线可读；认证失效保留缓存并提示重登，网络故障不改成认证失效。
- Android Keystore AES-GCM 加密会话，Go 不写 `cookie.json`。账号切换清空旧快照和提醒记录；注销清空会话与数据库内容。应用备份关闭。
- 本地通知由系统授权控制；发送失败不会记为已发送。周期刷新间隔为 15 分钟，受系统调度、省电和厂商限制，不能承诺精确到分钟。强行停止应用后需重新打开。
- Android 暂未实现 WebUI 的跨课堂分组、远程推送配置、成绩即时更新或多端同步。学习日志仍沿用现有缓存策略。

每次刷新前向 Go 引擎传入设备 IANA 时区，绑定内置 tzdata；各引擎互不修改全局时区。
静默窗口和通知文案使用同一时区，跨夏令时顺延按日期计算。后台刷新先创建可取消请求，
再登记 Worker；提前停止和登记期间停止都保留取消状态，只取消对应 Worker 的请求。

官方登录页使用填满可用区域的 WebView。alpha.2 将整个桌面页缩小后，用户实机反馈二维码过小，
登录窗口的 FLAG_SECURE 又使截图无法用于微信相册识别。alpha.3 允许登录页截图；仅官方 `/web`
登录页注入 `assets/login-mobile.js`，将 viewport 改为设备宽度，600px 以下重排登录卡片，
二维码框最大 240px，保留登录方式切换、协议和页面滚动。允许双指缩放；不改变认证和导航白名单。
加载失败可重载；WebView 调试仅 debug 开启。

同机可截图后切换微信「扫一扫 → 相册」尝试识别，确认后返回应用点击「已登录」。这条流程仍需
用户验证微信是否接受该登录码；另一台设备扫码也是可选方式。截图不会由应用自动保存或上传，
登录页销毁时仍清理 WebView 会话。

alpha.4 修复当前 basic-info 返回 `id` 而旧解析只读取 `user_id` 导致的有效会话拒绝；
兼容逻辑留在 Go DTO，仍校验非零身份。点击「已登录」时显示校验进度。

## 固定构建环境

| 工具 | 版本 |
|---|---|
| Go（移动模块） | 1.27.3 |
| golang.org/x/mobile | `v0.0.0-20261011034528-197770444867` |
| JDK | 17 |
| Gradle / Android Gradle Plugin | 8.11.1 / 8.9.1 |
| Kotlin / Compose compiler plugin | 2.1.20 |
| SDK / build-tools | 35 / 35.0.0 |
| NDK | 28.2.13676358 |
| 最低系统 / 目标系统 | Android 8.0（API 26）/ Android 15（API 35） |
| ABI | arm64-v8a、x86_64 |

具体依赖以 Gradle 文件与移动模块 go.mod/go.sum 为准；构建时检查 SDK、NDK 和 JDK。Android 有 JNI / cgo 工具链，不改变原守护进程的纯 Go 发行要求。

在 Android SDK 中准备对应组件，并把 JDK 17 的路径设为 `JAVA_HOME`、SDK 路径设为 `ANDROID_HOME`。构建脚本在仓库外 ASCII 路径复制编译所需源码，避免中文路径影响原生工具，不复制运行数据或设计提示词。

```powershell
pwsh -File apps/android/build.ps1 -Go '<移动 Go 可执行文件>' -Stage '<仓库外 ASCII 构建目录>'

# 连接单台测试设备；多台设备时用 ANDROID_SERIAL 指定目标。
pwsh -File apps/android/build.ps1 -Go '<移动 Go 可执行文件>' -Stage '<同一构建目录>' -Test -SkipBinding
```

APK 与 SHA-256 在 `dist/android/`，均不入 Git。`-SkipBinding` 只用于已生成 AAR 且 Go 代码未变化的迭代；完整验收必须重新生成 AAR。debug 包名带 `.debug`，可与发行包并存。

## 签名与升级

release 签名只通过以下环境变量传入，不写 `local.properties`、示例或脚本常量：

```text
YKT_ANDROID_KEYSTORE
YKT_ANDROID_STORE_PASSWORD
YKT_ANDROID_KEY_ALIAS
YKT_ANDROID_KEY_PASSWORD
```

设置后运行 `build.ps1 -Variant Release`。内测签名与正式发行必须有清晰衔接；同包名升级要求同一签名和递增 versionCode。每次交付记录 APK SHA-256、签名证书指纹、版本、源码提交及验收结果。正式分发前验证签名、zipalign 与 native ELF 的 16 KiB 对齐。

密钥、密码、备份及本机定位留在仓库外。签名私钥需要加密备份，密码另存可信密码管理器；只有 Windows DPAPI 密文不能作为跨机器恢复方案。先在隔离目录恢复密钥并比较证书指纹，再认为备份可用。用户数据不随 APK 备份；重新安装通常需要重新登录。

## 验收与待证据

Go 回归覆盖刷新、快照重开、账号切换中断、注销、通知失败重试、认证/网络分类、取消和凭据轮换，新增回归要求实际反向验证。设备测试使用合成账号与任务，不连接真实账号。

设备测试覆盖 AAR 加载、SQLite 重开与 nullable 成绩、Keystore 密文、系统通知权限及唯一 Worker。
MuMu Android 15 的 11 项设备测试通过，覆盖设备时区、预取消与 Worker 停止竞争、登录布局和截图许可。
昨日 7 项实现破坏、今日 4 项 Kotlin 实现破坏均得到断言失败并恢复全绿。
今日 Go 时区、DST、通知时间、取消和认证恢复另做 10 项反向验证；证据留在仓库外。
alpha.3 另实际恢复截图禁令、恢复整页缩小、扩大切换角标造成二维码遮挡，三项回归均取得断言失败，
恢复后 11 项全绿；320dp 窄屏的两个登录测试通过。官方匿名页面的 360dp 检查中二维码为 238dp，
无横向溢出；使用合成占位替换二维码后，原生截图有正常内容。设备证据留在仓库外。
两个 ABI 的 ELF 加载段均为 16 KiB 对齐；这不能替代真实 16 KiB 页设备运行验收。
构建、安装与待办见当前 HANDOFF；不能把模拟器通过替代真机验收。

alpha.2 已用原内测签名从 alpha.1 覆盖升级，在 MuMu Android 15 启动并打开登录页；
签名和 16 KiB zipalign 检查通过。官方扫码的真实账号验证与 Cookie 失效到重登的完整送达流程未完成。

2026-11-09 远端 Android CI `42547781128`（`a10597a`）在 Linux 完成 AAR、debug APK 构建及 lint；
核心多平台 CI `42547208400`（`194318b`）通过。流水线不上传签名密钥，也不替代本机的设备测试。

alpha.3 源码提交 `4ca6faf` 的 Android CI `42556426484` 同样通过 Linux AAR、debug APK 和 lint。
本机签名包由该干净提交重建，SHA-256 与交付包一致；Go 核心未变化，本机复用 alpha.2 的干净 AAR。

正式发行仍需真实扫码登录、物理设备通知、重启恢复、进程回收、Doze / 厂商省电及连续运行证据。官方网页登录失败时先保留可复现流程与脱敏现象，不能通过放宽域名或导入明文凭据掩盖问题。

alpha.4 的当前 / 旧版身份字段、优先级、缓存重开回归通过；移除当前字段兼容与覆盖旧字段
两项反向验证均取得断言失败。Go AAR 已重新生成，11 项模拟器设备回归和 debug lint 通过。
登录布局测试先阻断外网，并等待清 Cookie 回调发起的初始导航，再加载合成页面，避免导航竞争；
禁用生产样式注入时断言失败，恢复后连续三次通过。真实扫码验收在设备回归结束后进行，
未将真实 Cookie、姓名、课程或任务内容写入测试或公开材料。
