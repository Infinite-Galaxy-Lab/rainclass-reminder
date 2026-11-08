# ADR 0001：Windows 与 Android 客户端

状态：建议采用；2026-11-08。此次交付为架构方向，尚未生成 GUI 安装包或 APK。

## 决策

保留现有 Go 提醒引擎与 WebUI。Windows GUI 选择 Wails 薄壳；Android 选择 Kotlin / Jetpack Compose，通过 `gomobile bind` 的窄接口调用 Go 业务核心。先完成 Android 依赖构建与后台通知验证，再开始完整界面开发。

| 方案 | 适配本项目的收益 | 代价与结论 |
|---|---|---|
| Wails | 复用 WebUI 与 Go，适合 Windows 窗口、托盘和生命周期 | 不把移动端支持作为 Android 发行承诺；用于 Windows GUI |
| Kotlin / Compose + Go AAR | 原生登录、通知权限与后台调度，复用 Go 类型映射和策略 | JNI 边界和 SQLite Android 兼容性需验证；Android 主方向 |
| Flutter + Go | 后续可统一移动 UI | 增加 Dart 与 Go 桥接、构建及调试成本，目前不选 |
| Fyne | UI 与业务均可用 Go，支持移动打包 | 登录 WebView、通知与后台机制仍需原生适配；保留为备选 |

Wails 官方仓库将 v3.0.0 列为稳定桌面版本，Android / iOS 仍属实验支持，不在稳定桌面的兼容保证内。桌面与移动端成熟度应分别核实，不能因为 Go 可以编译 Android 就认为整个桌面程序可直接移植。采用的具体 Wails / Kotlin / NDK 版本在验证构建时锁定，避免文档先承诺未经测试的组合。

## 依赖边界

```text
CLI / WebUI / Wails / Android Worker
                  ↓
应用用例：刷新、查询、提醒决策、登录态更新
                  ↓
domain（纯逻辑） + ports（存储、抓取、通知、时钟）
                  ↑
ykt / store / auth / 各平台通知与凭据适配器
```

这是目标结构，不代表已有 `internal/application`。现有 `scheduler` 的编排继续运行；有第二个真实调用方时，才将共享用例从 CLI 装配中提取出来。平台依赖留在入口与适配器，`domain` 不依赖 UI、HTTP、SQLite 或 Android。

计划目录及责任：

| 路径 | 责任与建立时机 |
|---|---|
| `internal/domain`、`ykt`、`store`、`auth` | 现有可复用层，保持业务类型与网页路由解耦 |
| `internal/application` | 在 GUI / mobile 接入时提取用例与端口；不装配桌面通知 |
| `bindings/mobile` | 首个 AAR 验证时建立，导出小型稳定 API；不直接暴露 domain 或 sql 类型 |
| `apps/desktop` | Wails 原型通过后建立独立模块，避免桌面工具链进入守护进程依赖 |
| `apps/android` | AAR 与后台验证通过后建立 Gradle 工程 |
| `deploy`、`tools` | 部署与诊断，不能变成业务逻辑的第二份实现 |

Go mobile 的初始边界只包含：打开应用、更新登录态、执行一次刷新、查询任务、取消/关闭。传递 UTF-8 JSON 或少量绑定支持的基础类型，结果带版本；错误只返回稳定分类，不把 cookie、完整 URL 或原始响应暴露给 UI。Java / Kotlin 对象不可跨调用长时间持有；线程安全、取消与重入在原型阶段验证。

桌面沿用一个后台引擎和一个数据库写入者。GUI 连接本机服务，关闭窗口不结束提醒；独立 WebUI 可继续使用。接入 GUI 前必须补服务发现、单实例管理和本地鉴权，凭据不能写入前端资源或放进 URL。暂不为 GUI 再启动一份调度器。

Android 的 Worker 调用一次用例后释放资源，由系统安排下一轮。WorkManager 周期任务最短间隔为 15 分钟，实际执行还会受 Doze 等因素影响；桌面的常驻轮询不作为 Android 保证。先提供尽力而为的本地提醒，并保留现有远端推送方案。更精确的本地定时、常驻前台服务及其权限分别评估，不在原型中默认申请。

凭据仅在本机登录流程与 Go 调用之间传递，持久化由 Android Keystore 支持的存储适配器负责。WebView 登录是否可用、Cookie 与 Go 请求是否同域且同态，必须通过真实流程验证；不假设复制桌面 `cookie.json` 就能成为移动端登录方案。

## 进入实现前的验证关卡

1. `gomobile bind` 产出 arm64 AAR，最小 Kotlin 应用可加载，执行合成数据的一次决策；列出 NDK、JDK、SDK 与 Go 固定版本。
2. 检查 Go mobile 的依赖闭包，排除 `beeep` / 桌面平台代码；验证当前 `modernc.org/sqlite` 在 Android 的构建、迁移与持久化，不能仅凭纯 Go 标签推断兼容。
3. 真机验证取消、并发、进程被杀后恢复、断网、登录失效、通知授权及系统省电；重复 Worker 不得重复提醒。
4. 登录流程验证只读抓取，抓取层仍只用 GET；不引入任何提交、答题或交卷接口。
5. 原型通过后才建立 UI 与发行流水线。失败时先记录具体阻塞点再调整绑定/存储适配器，不为绕过失败复制一份业务算法。

## 官方依据

- Wails：<https://v3.wails.io/>，采用前复查目标平台支持状态。
- Wails 官方仓库平台说明：<https://github.com/wailsapp/wails/blob/master/v3/README.md>。
- Go mobile：<https://go.dev/wiki/Mobile>，`gomobile bind` 与 Android AAR。
- Android 周期任务：<https://developer.android.com/reference/androidx/work/PeriodicWorkRequest>。
- Android 后台任务：<https://developer.android.com/develop/background-work/background-tasks/persistent/getting-started/define-work>。
- Fyne 移动打包：<https://docs.fyne.io/started/mobile.html>。
