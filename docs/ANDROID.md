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

当前开发版本为 `0.1.0-alpha.8`。alpha.4 在 MuMu Android 15 已由用户真实扫码登录，完成任务刷新，
结束进程再打开后登录与缓存保留；2026-11-09 用户另确认物理手机可正常登录并跳转。
物理手机的后台通知与恢复仍待验收。
设备时区已接入；不能将模拟器回归通过视为后台通知可靠性验收。

- 原生任务列表、手动刷新、搜索与状态筛选；成绩区分缺失、零分和小数。
- alpha.5 默认待办排除已截止、已完成和课件；未来安排按最近时间升序，已截止和完成记录按最近时间倒序。
  课件忽略考核截止时间，课堂按开课时间展示；仅凭开始时间不能推断结束。未设时间单列，同时间按课程、标题、ID 稳定排序。
  支持 24h 截止筛选、课程筛选与组内按课程排序。统计卡显示全量计数，点击清除搜索与课程条件，进入对应状态。
  课程名筛选不等于课堂身份筛选，Android 当前不合并跨课堂记录。
- 缓存离线可读；认证失效保留缓存并提示重登，网络故障不改成认证失效。
- 登录成功先显示本地缓存，立即执行课程同步；普通启动恢复加密会话与缓存，不强制重新登录。
- 前后台重叠刷新复用等待期间完成的成功结果，失败仍允许重试。缓存全冷时章节、详情与学习日志
  请求较多，300ms 发送间隔与最多 4 个在途请求保持不变；首次同步仍可能需要几分钟。
  同步期间保留列表与操作，单轮 Go 网络阶段上限 5 分钟；取得串行锁之前的等待不计入该上限。
- Android Keystore AES-GCM 加密会话，Go 不写 `cookie.json`。账号切换清空旧快照和提醒记录；注销清空会话与数据库内容。应用备份关闭。
- 本地通知由系统授权控制；发送失败不会记为已发送。周期刷新间隔为 15 分钟，受系统调度、省电和厂商限制，不能承诺精确到分钟。强行停止应用后需重新打开。
- Android 暂未实现 WebUI 的跨课堂分组、远程推送配置、成绩即时更新或多端同步。学习日志仍沿用现有缓存策略。

每次刷新前向 Go 引擎传入设备 IANA 时区，绑定内置 tzdata；各引擎互不修改全局时区。
静默窗口和通知文案使用同一时区，跨夏令时顺延按日期计算。后台刷新先创建可取消请求，
再登记 Worker；提前停止和登记期间停止都保留取消状态，只取消对应 Worker 的请求。

备用的官方登录页使用填满可用区域的 WebView。alpha.2 将整个桌面页缩小后，用户实机反馈二维码过小，
登录窗口的 FLAG_SECURE 又使截图无法用于微信相册识别。alpha.3 允许登录页截图；仅官方 `/web`
登录页注入 `assets/login-mobile.js`，将 viewport 改为设备宽度，600px 以下重排登录卡片，
二维码框最大 240px，保留登录方式切换、协议和页面滚动。允许双指缩放；不改变认证和导航白名单。
加载失败可重载；WebView 调试仅 debug 开启。

旧版曾提供截图后到微信相册识别的提示；当前真机反馈微信拒绝相册登录码，不能据应用允许截图声称同机可用。
另一台设备扫码也是可选方式。截图不会由应用自动保存或上传。切后台保留当前 WebView，返回时
恢复检查；横竖屏、屏幕尺寸变化保留扫码页，Activity 重建不清 Cookie。新登录流程与结束登录时
清理网页数据，已认证会话另存于 Keystore 支持的加密存储。系统回收进程后未完成的二维码可能失效。
alpha.6 的真实后台扫码复验失败：用户已在微信确认成功，返回仍停在登录页。该问题保持未解决，不能用合成生命周期测试代替。

alpha.4 修复当前 basic-info 返回 `id` 而旧解析只读取 `user_id` 导致的有效会话拒绝；
兼容逻辑留在 Go DTO，仍校验非零身份。点击「已登录」时显示校验进度。

2026-11-09 对匿名官方静态页面的检查显示：默认 App 扫码采用一次最长 180 秒的 HTTP 确认，
旧 WebSocket 实现已注释；微信 iframe 是另一条登录路径。两条路径的外层代码均无返回前台的恢复逻辑。
这只证明公开页面行为，不证明服务端或跨域微信 iframe 的内部机制；Android 继续使用官方页面，
仅补充本机生命周期与会话检查，不另实现认证接口。

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

构建默认执行对应版本的 JVM 单元测试。`-Test` 运行 `connectedVerificationAndroidTest`，
使用独立 `.verification` 包名和私有数据目录，不卸载或清理用户已登录的 `.debug` / 发行包。
该变体复用相同生产源码与 debug 配置；设备测试使用合成数据。

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

物理手机登录跳转已由用户确认；正式发行仍需通知、重启恢复、进程回收、Doze / 厂商省电及连续运行证据。官方网页登录失败时先保留可复现流程与脱敏现象，不能通过放宽域名或导入明文凭据掩盖问题。

alpha.4 的当前 / 旧版身份字段、优先级、缓存重开回归通过；移除当前字段兼容与覆盖旧字段
两项反向验证均取得断言失败。Go AAR 已重新生成，11 项模拟器设备回归和 debug lint 通过。
登录布局测试先阻断外网，并等待清 Cookie 回调发起的初始导航，再加载合成页面，避免导航竞争；
禁用生产样式注入时断言失败，恢复后连续三次通过。真实扫码验收在设备回归结束后进行，
未将真实 Cookie、姓名、课程或任务内容写入测试或公开材料。

alpha.4 签名包由干净源码提交 `1d4efc4` 构建，Go AAR 的输入源码与该提交核对一致。
原证书覆盖升级 alpha.3 并启动通过；APK 签名、zipalign 与双 ABI ELF 的 16 KiB 对齐通过。
SHA-256 为 `da3bd6c0583b1f6e685ca75ffd235a95f0030e1eba7b809cf884534e9766a63e`。
核心多平台 CI `42559210215` 与 Android CI `42559191033` 均通过，同一源码对应 Linux AAR、debug APK 和 lint。

alpha.5 新增 7 项 JVM 回归，覆盖时间排序、稳定同名记录、24h 边界、课件、课堂开始时间、
组合筛选和跨年文案；13 项 Android 15 设备测试、debug / verification lint 通过。
7 项逻辑实现破坏与 2 项交互破坏取得断言失败，恢复后全部通过。320dp、1.3 倍字体、
零分、分别显示课堂记录、空搜索恢复及原链接回调使用合成数据验证。
本轮 Go 核心未变，沿用 alpha.4 已核对的 AAR；发行时再次核对输入文件与 AAR 哈希。

签名 alpha.5 来自干净提交 `283727d`，35,289,995 字节；SHA-256：
`15d74ad628718e8d576261d3f5fb45b25a442757fb1e16846c7aa51f2172061f`。
原证书、versionCode 5，release 单元测试和 lint、验签、zipalign、双 ABI 四个 native 库的
ELF 16 KiB 对齐通过；MuMu 覆盖升级 alpha.4 发行包启动正常，调试包另升级保留原登录。
同一源码的 Android CI `42563634298` 通过 Linux AAR、debug APK、JVM 回归与 lint。

alpha.6 新增前台恢复检查、登录结果触发立即同步、旧缓存取消和重叠刷新合并。
10 项 JVM 与 22 项 Android 15 设备回归通过；12 项实现破坏分别取得断言失败，包含有界检查、
重建保 Cookie、登录即同步、旧缓存、失败保留、新 Vault/Engine 恢复、重叠成功复用、失败重试及会话切换取消。
持久化新增测试证明新对象和新引擎可读取已落盘会话，不将同进程重开称为进程回收验收。
本轮仍复用未变化的 Go AAR；后台扫码实际微信流程、回收后的未完成二维码和认证中返回时序待实测。

alpha.6 签名包来自干净源码 `295f47d`，versionCode 6，35,290,015 字节，SHA-256：
`8c4cc571d1dbdd3d25aa0e51c54453e3eb888565776ca0018b14511ce20f2c1a`。
release JVM 测试、lint、原证书验签、zipalign 和双 ABI native ELF 的 16 KiB 对齐通过。
MuMu 覆盖安装调试包后加密会话文件哈希未变；发行包覆盖安装成功，版本核对为 alpha.6。
此项证明升级保留存储，不将其等同于新版本进程启动或服务端会话有效期验证。

2026-11-09 后续取证：验证包返回栈出现两套 Main/Login。辅助脚本曾用无 MAIN/LAUNCHER 的
显式 Intent 启动根页面，随后从桌面图标返回可能生成新根页面；尚不能把此现象认定为真实手机根因。
已改用标准 MAIN + LAUNCHER 启动进行复验，运行时只收集请求类别、HTTP 状态及布尔状态。
两个旧 WebView 均显示二维码失效且没有 sessionid；官方失败遮罩只能获取新二维码，不能沿用旧确认 token。
alpha.6 Android CI `42567795205` 重跑通过 Linux AAR、APK、JVM 测试和 lint；首次运行受 Gradle 下载 504 影响。

手动扫码验证应从桌面图标启动，或使用等价命令，避免用仅有组件名的启动制造不同任务栈：

```powershell
adb -s '<测试设备>' shell am start -a android.intent.action.MAIN -c android.intent.category.LAUNCHER -n lab.infinitegalaxy.rainclass.verification/lab.infinitegalaxy.rainclass.MainActivity
```

验证包完成真实扫码后，不再直接运行会清除该包会话的合成设备测试。

## alpha.7：原生微信扫码

alpha.6 在正常启动、单个登录页下仍无法完成后台扫码；用户已在微信确认成功。
确认请求超时且没有会话，一次同 token 恢复实验也失败，因此不继续增加 Cookie 检查次数。

默认登录改为原生二维码和应用持有的微信轮询，协议复用现有 Go `auth.QRLogin`。
登录任务由 ViewModel 保留，切微信后仍等待，返回前台交付结果并立即刷新课程。
退出或换码取消旧请求，认证失败可重试；已有会话继续使用 Keystore 加密恢复。
官方 WebView 作为备用入口保留，截图仍允许，二维码不由应用自动保存。
系统回收进程后须重新取码，后台省电可靠性不在本次修复的验证范围。
见 [ADR 0002](adr/0002-android-native-qr-login.md)。

设备回归使用 `.verification.synthetic` 独立包，保护实际扫码包与已有登录存储。
27 项设备回归覆盖 Home 后从启动器返回、配置重建、取消、旧结果隔离和身份校验失败。
初次原生扫码真实复验仍失败；随后补齐官方轮询 `last` 回传，用户已确认 MuMu 后台扫码、返回首页成功。
合成测试不能代替真机微信流程；手机相册识码限制与后台结果交付分别验收。

alpha.7 签名包来自干净源码 `0125d92`，versionCode 7，35,507,807 字节；SHA-256：
`b18d3e5f99806206cf81bfc99d4870a29f8466e1b9f0e48ed65b949b46d528a4`。
完整重新生成 AAR，88 个 Go 与模块输入核对一致；验签、zipalign 与双 ABI ELF 16 KiB 对齐通过。
调试包覆盖升级前后加密会话文件哈希一致，发行包覆盖安装成功；尚未据此验收新进程恢复。
核心 CI `42587921462`、Android CI `42587810463` 均对应 `0125d92` 并通过。

## alpha.8：手机扫码协议修正与单手机实验

将已确认的 `last` 轮询修正重新构建为 arm64-v8a / x86_64 双 ABI AAR，
手机发行包不复用仅 x86_64 的隔离诊断 AAR，不包含仓库外 `RAINLOGIN` 插桩。
模拟器后台扫码已由用户确认；实机微信相册不接受登录码，单手机流程仍待验证。

验证包的“单手机登录实验”会复制当前二维码对应的官方确认链接。
在微信“文件传输助手”粘贴、发送给自己并点开，若微信显示登录授权则确认后返回。
若仍要求扫码或页面空白，记录现象，不将该方法宣称为已支持；不需要提交链接或截图中的账号数据。
链接只经用户主动操作进入剪贴板，带敏感标记；换码、结束和失败返回前台后清除本入口的旧内容。
二维码和链接均不写仓库或日志。发行包隐藏实验入口；两个包有独立登录存储。

已核查的 Live-QR（`Dynesshely/Live-QR`，提交 `6ff1e9205d7189278eab7707f8bd6711d2f62b69`）
将摄像头识别的文本广播给观看端重绘，不能用同一手机摄像头扫描自身屏幕。
其审计默认留存明文二维码内容，不将其公共实例接入本项目认证。
原生微信 OpenSDK 还要求本应用的移动 AppID、包名与签名审核，
并需要雨课堂认可相应授权凭据；当前没有此对接，不能复用网站扫码 AppID 冒充原生授权。

参考：

- [Live-QR 源码](https://github.com/Dynesshely/Live-QR)
- [微信移动应用登录](https://developers.weixin.qq.com/doc/oplatform/Mobile_App/WeChat_Login/Development_Guide.html)
- [微信 Android 接入](https://developers.weixin.qq.com/doc/oplatform/Mobile_App/Access_Guide/Android.html)
- [微信移动应用注册](https://developers.weixin.qq.com/doc/oplatform/Mobile_App/guideline/create.html)

验证：212 个 Go 测试函数全量通过，vet、gofmt 无输出；Android 29 项合成设备测试、JVM 测试和 lint 通过。
新增回归的 2 项 Go 与 3 项 Android 实现破坏均取得断言红，恢复后全绿。
上述检查仅验证应用行为、URL 边界与剪贴板清理，不代表微信接受手动点击确认链接。
