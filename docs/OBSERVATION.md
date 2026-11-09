# P2：被动观察

## 当前运行与启动安排

2026-11-09 重启后的原任务退出失败。09:01:55 首次采集时即认证失效，尚无最近成功样本；
不能把本地导入至首次失败的约 21 小时当作真实 Cookie 寿命。

本机计划任务 `YktReminder` 已于 09:37:28 手动触发并保持运行，面板与观察文件检查通过。
当前部署改为 `%APPDATA%\ykt-reminder\runtime`，因为任务进程无法访问原 Local 部署目录；
原配置、数据目录和观察窗口保留，目录 ACL 只允许当前用户与 SYSTEM。
这次验收覆盖计划任务启动链路；下次用户登录触发仍待验。当前须用户在面板重登才能取得成功样本。

本机设置为 **2026-11-09 00:00:00 +08:00 之后的首次用户登录**，延迟 30 秒随项目启动。不是午夜定时唤醒，也不会让关机的电脑自动开机。配置、数据目录沿用已有路径，实际值只在用户私有部署设置中。

`deploy/install-observation.ps1` 准备二进制、隐藏启动器和计划任务；`deploy/start-observed.ps1` 同步等待主进程并返回退出码，计划任务才能判断失败。指定已有任务名升级，避免重复自启。

```powershell
# 在仓库根目录，替换成现有配置/数据目录；安装后不会立即启动。
powershell -ExecutionPolicy Bypass -File deploy/install-observation.ps1 `
  -BinaryPath dist/rainclass-reminder-observed.exe `
  -ConfigPath '<现有 config.yaml>' -DataDir '<现有数据目录>' `
  -TaskName '<已有任务名>' -NotBefore '2026-11-09T00:00:00+08:00'
```

安装器默认部署目录为 `%LOCALAPPDATA%\RainClassReminder`，可用 `-InstallDir` 指定；本机已改用上述 Roaming 私有目录。
`settings.json` 存路径与时间，不存凭据；观察输出在部署目录的 `observations` 子目录。
旧任务 XML 与设置在原部署目录的 `rollback`。手动运行其他二进制不自动继承这些设置；
要使用同一启动入口，执行部署目录里的 `start-observed.ps1 -SettingsPath <settings.json>`，并确保没有另一实例运行。

## 项目请求与登录态

不开启时没有观察输出。`YKT_OBSERVE_FROM` 是启用下限时间（RFC3339）；到达该时间后，程序启动时创建观察器。`YKT_OBSERVE_DIR` 可指定仓库外输出目录，默认数据目录下的 `observations`。

只记录现有请求，不增加轮询、不增加认证探测、不改业务结果：

| 文件 | 内容与保留范围 |
|---|---|
| `capture-window.json` | 首次实际采集的 UTC 时间，重启沿用 |
| `requests-YYYY-MM-DD.jsonl` | 前 24 小时内的请求类别、状态、耗时、结果分类、两种 Set-Cookie 是否出现 |
| `auth-YYYY-MM.jsonl` | 原有 BasicInfo / Courses 调用的认证结果；24 小时后继续记录，直到禁用 |

每个日志文件上限 32 MiB；请求达到上限后丢弃，认证日志达到上限会告警一次但不影响提醒。没有自动删除历史观察文件，请定期在本地归档。目录与文件在 Unix 用 0700 / 0600；本机 Windows 部署根目录通过 ACL 限制为当前用户与 SYSTEM。

不保存 URL、查询、请求头、响应体、Cookie 值、账号、课程名或原始错误。端点与结果均采用固定白名单，未知端点为 `other`。HTTP 200 不代表业务成功，因此认证证据在解析业务包之后单独记录。HTTP 层请求的 `ok` 只表示该层通过，不代表最终认证成功。

报告命令：

```powershell
# 本机已迁移后的目录；其他机器以 settings.json 中的 observation_dir 为准。
python tools/observe_report.py "$env:APPDATA\ykt-reminder\runtime\observations"
```

报告保留程序请求与浏览器请求的独立统计，忽略未知字段和坏尾行，不输出原始记录或路径。认证按本地 `acquired_at` 分组：最近成功、首次明确认证失败、失败后是否再次成功。`upstream`、断网、取消均不能算登录失效。

**这些数据测的是本地导入/保存后的已观察可用时间，不是服务端签发时刻到失效的精确寿命。** `UpdatedAt` 来自本地导入/登录保存；某些用户提示信息保存也可能更新时间。电脑关机、休眠和网络故障期间没有证据，失效只能落在最近成功与首次明确失败之间；若失败后又成功，须先排查暂时故障/重新登录。单独出现 Set-Cookie 也不能证明成功续期，其值可能未变或表示删除。

禁用观察：把部署 `settings.json` 的 `observation_enabled` 改为 `false`，下次重启项目生效；手动运行时移除 `YKT_OBSERVE_FROM`。不要为禁用观察随意删除登录态或数据库。若要开始新一轮 24h 观察，在程序退出后归档整个观察目录并指定新目录，避免混合两轮数据。

## 官方网页 Network：另一个证据来源

项目请求不能解释官方网页为什么可能更持久。`tools/observe_browser.py` 可通过本机 CDP 监听**用户已经打开的官方网页**，不导航、不登录、不刷新、不发业务请求，不读取响应体或 Cookie 值。用户在网页的正常操作可能触发写请求，脚本只观察其方法/状态，不执行这些操作。

首次准备在仓库外虚拟环境安装 Playwright；不需要由 Playwright 下载浏览器，使用本机 Edge：

```powershell
python -m venv "$env:APPDATA\ykt-reminder\runtime\observer-venv"
& "$env:APPDATA\ykt-reminder\runtime\observer-venv\Scripts\python.exe" -m pip install playwright

# 使用单独的私有 profile，避免改动日用浏览器；用户在该窗口自行登录。
$edge = "${env:ProgramFiles(x86)}\Microsoft\Edge\Application\msedge.exe"
$profile = "$env:APPDATA\ykt-reminder\runtime\observer-edge-profile"
Start-Process -FilePath $edge -WindowStyle Normal -ArgumentList @(
  '--remote-debugging-address=127.0.0.1', '--remote-debugging-port=9222',
  "--user-data-dir=`"$profile`"", 'https://changjiang.yuketang.cn/'
)
& "$env:APPDATA\ykt-reminder\runtime\observer-venv\Scripts\python.exe" `
  tools/observe_browser.py --cdp http://127.0.0.1:9222 `
  --output "$env:APPDATA\ykt-reminder\runtime\observations"
```

只允许回环 HTTP CDP 地址，输出只接收 `yuketang.cn` / `xuetangx.com` 及其子域 HTTPS 事件。保留端点类别、方法、状态、到响应头的耗时与两种 Set-Cookie 存在标记；未知路径统一 `other`。没有完整 HAR、正文、请求 Cookie 或 WebSocket 帧，所以仅能发现续期线索，不能单凭这份输出还原所有官方逻辑。浏览器 401/403 也可能是权限错误，不能直接当作全账号失效。

最多记录从首次浏览器采集起的 24h，`browser-window.json` 跨脚本重启保留窗口；关闭浏览器或 Ctrl+C 结束。脚本退出不关闭用户窗口。CDP 能访问浏览器会话，应只绑定回环、使用独立 profile，观察结束关闭该窗口。

**项目侧观察已恢复；官方网页侧仍需用户打开上述专用窗口并登录后运行监听命令。**
当前没有网页侧记录，不把项目请求当作官方网页全天观察证据。
