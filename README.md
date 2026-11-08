<p align="center">
  <img src="docs/assets/icon.png" width="128" alt="RainClass Reminder">
</p>

<h1 align="center">RainClass Reminder</h1>

---

<p align="center">
  <b>长江雨课堂的截止提醒助手 · 把作业和考试推到手机上</b><br>
  只读守护进程 · 多通道推送 · 本地 Web 面板 · 纯 Go 单文件部署<br>
  桌面端先行，移动端 App 发行版在计划中
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.23-00ADD8?style=flat-square&labelColor=1f2328&logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/badge/Platform-Windows%20%7C%20Linux%20%7C%20macOS-4c8bf5?style=flat-square&labelColor=1f2328" alt="Platform">
  <img src="https://img.shields.io/badge/Deploy-Single%20Binary%20%7C%20Docker-2ea44f?style=flat-square&labelColor=1f2328" alt="Deploy">
  <img src="https://img.shields.io/badge/Notify-Feishu%20%7C%20Webhook%20%7C%20Email-e36209?style=flat-square&labelColor=1f2328" alt="Notify">
  <img src="https://img.shields.io/badge/License-MIT-d4a72c?style=flat-square&labelColor=1f2328" alt="License">
</p>

## 为什么

雨课堂（Rain Classroom）只在打开 App 或网页时展示任务，没有「截止前主动推送」。课程一多，漏交作业、错过考试就成了常态。

RainClass Reminder 补上这一环：一个常驻后台的只读进程，按你设定的时间窗把即将到期的任务推出来。

只读是它的第一设计约束——不提交、不答题、不交卷，全部请求为 GET。

## 特性

| | |
|---|---|
| **只读** | 全部请求为 GET，不提交、不答题、不交卷、不改服务端状态 |
| **多通道推送** | 飞书群机器人 / 桌面通知 / 通用 Webhook（Bark、Server酱、企微、钉钉、TG）/ SMTP 邮件 |
| **多级提醒** | 默认截止前 1 天、6 小时、1 小时、15 分钟各一次；课堂类额外提前 10 分钟开课提醒 |
| **改期自动重算** | 去重键包含截止时间，老师改期即重新计时 |
| **断网不轰炸** | 多个窗口同时错过时只补发最紧急的一条 |
| **静默时段** | 23:00–07:00 内，1 小时以上的提醒顺延到 07:30；15 分钟内的照发 |
| **本地 Web 面板** | 任务倒计时、微信扫码登录、推送历史，默认只监听 `127.0.0.1` |
| **单文件部署** | 纯 Go 静态链接，约 13 MB，常驻内存 25–35 MB，空闲 CPU ≈ 0% |

## 快速开始

### 1. 编译

```bash
git clone https://github.com/Infinite-Galaxy-Lab/rainclass-reminder.git
cd rainclass-reminder

go build -o rainclass-reminder ./cmd/rainclass-reminder   # 仅本机
bash deploy/build.sh                                      # 六个平台交叉编译
```

需要 Go 1.23 或更高。

### 2. 登录

**微信扫码**（推荐，无需手动复制 cookie）：

```bash
./rainclass-reminder auth login
```

终端会打印二维码，微信扫码即可。登录态存到 `<数据目录>/cookie.json`，权限 0600。

**导入浏览器 Cookie**：

浏览器登录 <https://changjiang.yuketang.cn>，按 F12 → Application → Cookies，复制 `sessionid` 与 `csrftoken`：

```bash
./rainclass-reminder auth import --cookie "sessionid=xxx; csrftoken=yyy"
```

### 3. 配置推送

以飞书为例：建一个只拉自己的群，群设置 → 群机器人 → 添加机器人 → **自定义机器人**，复制 Webhook 地址。

```bash
export YKT_FEISHU_WEBHOOK="https://open.feishu.cn/open-apis/bot/v2/hook/xxx"
export YKT_FEISHU_SECRET="若开启了签名校验才需要"
```

> 不要在机器人上启用 IP 白名单或关键词限制，否则请求会被拦截。

凭据优先从环境变量读取。也可写入配置文件（Windows `%AppData%\rainclass-reminder\config.yaml`，Linux/macOS `~/.config/rainclass-reminder/config.yaml`）。全部配置项见 [`configs/config.example.yaml`](configs/config.example.yaml)，其它通道见 [docs/CHANNELS.md](docs/CHANNELS.md)。

### 4. 验证

```bash
./rainclass-reminder tasks          # 打印任务列表，可与网页端逐条核对
./rainclass-reminder notify test    # 手机应收到一张测试卡片
./rainclass-reminder run --dry-run  # 跑一轮，只打印将要发送的内容
```

### 5. 常驻运行

```bash
./rainclass-reminder run
```

启动后同时开一个本地面板：<http://127.0.0.1:8787>

<details>
<summary>开机自启</summary>

**Windows**（计划任务）：

```powershell
powershell -ExecutionPolicy Bypass -File deploy\install-windows.ps1
```

**Linux**（systemd）：

```bash
sudo cp deploy/rainclass-reminder.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now rainclass-reminder
```

**Docker**：

```bash
cd deploy && docker compose up -d
```

</details>

## 命令

| 命令 | 说明 |
|---|---|
| `run` | 启动常驻提醒服务（含面板） |
| `run --once` | 只跑一轮后退出 |
| `run --dry-run` | 只打印将要发送的内容，不实际推送 |
| `tasks` | 拉取并列出任务 |
| `tasks --json` | JSON 输出，便于脚本处理 |
| `auth login` | 微信扫码登录 |
| `auth import --cookie "..."` | 从浏览器 Cookie 导入 |
| `auth status` | 查看登录态并在线校验 |
| `auth clear` | 清除本地登录态 |
| `notify test` | 发送测试消息 |
| `probe` | 只读探查接口字段，输出脱敏快照 |
| `web` | 只启动面板，不跑定时提醒 |
| `version` | 显示版本 |

所有命令都支持 `--config <path>`。

## 提醒策略

默认在截止前 **1 天 / 6 小时 / 1 小时 / 15 分钟**各提醒一次。

- **改期重新计时** —— 去重键 = `taskID | 窗口 | deadline`，截止时间变化即视为新提醒
- **休眠后不轰炸** —— 多个窗口同时错过时，只发最紧急的一条，其余记为已处理
- **静默时段** —— 23:00–07:00 内，超过 1 小时的提醒顺延到 07:30，15 分钟内的照发
- **已完成跳过** —— 从学习日志取完成状态，完成后不再提醒
- **单轮上限** —— 超过 8 条合并为摘要

策略参数在 `policy` 段调整。

## 架构

```
cmd/rainclass-reminder      入口
internal/cli                子命令与依赖装配
internal/scheduler          编排：抓取 → 决策 → 发送 → 记账
internal/domain             纯领域层（任务、里程碑、策略），零 I/O
internal/ykt                唯一知道雨课堂 API 的层（client / dto / mapper）
internal/notify             推送层（Notifier 接口 + 飞书 / 桌面 / Webhook / 邮件）
internal/store              持久化（Repository 接口 + SQLite）
internal/auth               登录态（cookie 导入 / 微信扫码 OAuth）
internal/netguard           SSRF 地址校验
internal/qrutil             二维码渲染
internal/webui              本地面板（embed 静态资源）
```

依赖方向严格单向，`domain` 不 import 任何其它 internal 包。设计取舍与数据流见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)。

## 安全

| 项目 | 做法 |
|---|---|
| 只读保证 | `internal/ykt` 只暴露 GET 方法，`endpoints.go` 里没有任何提交类路径 |
| 凭据存储 | cookie 落盘 0600，日志中脱敏为 `sess***ab` |
| SSRF 防护 | 推送地址发送前校验：仅 https、域名白名单、拒绝环回/私有/CGNAT/云元数据，含 DNS 重绑定防护 |
| 面板暴露 | 默认只监听 `127.0.0.1`；绑定非回环地址时必须配置 token，否则拒绝启动 |
| 密钥管理 | 支持环境变量注入，避免明文写入配置文件 |

完整说明见 [SECURITY.md](SECURITY.md)。

## 文档

- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) —— 分层、数据流与关键设计决策
- [docs/API_NOTES.md](docs/API_NOTES.md) —— 雨课堂接口真实结构
- [docs/CHANNELS.md](docs/CHANNELS.md) —— 各推送通道的配置方法
- [docs/EVOLUTION.md](docs/EVOLUTION.md) —— 迭代记录与踩坑归档
- [docs/HANDOFF.md](docs/HANDOFF.md) —— 交接说明与当前待办

## 开发

```bash
go test ./...      # 全部单测
go vet ./...
gofmt -l ./cmd ./internal
```

接口字段变更时：先 `rainclass-reminder probe` 生成脱敏快照，对照 [docs/API_NOTES.md](docs/API_NOTES.md) 更新 `dto.go` / `mapper.go`，再用真实账号验证。

## 路线图

- [x] 只读抓取与分级提醒
- [x] 四个推送通道
- [x] 本地 Web 面板与扫码登录
- [ ] 移动端 App 发行版（Android / iOS）
- [ ] 跨课堂同名任务合并
- [ ] 面板分数展示（`score/total_score`）

## 常见问题

**cookie 多久失效？**
不固定，通常数天到数周。失效时程序会发一条告警（只发一次），面板上直接给出二维码。雨课堂不会通过 Set-Cookie 续期 `sessionid`，过期只能重新登录。

**能否部署到服务器、登录在本地做？**
可以。本地跑 `auth login`，把生成的 `cookie.json` 复制到服务器的数据目录即可。

**为什么不提醒某些讨论或课堂？**
这两类没有统一的截止时间字段。有 `start_time` 的课堂走开课提醒；既无截止也无开课时间的任务只在面板展示，不推送——不猜时间。

**会封号吗？**
本工具只做与网页端相同的只读请求，默认 10 分钟一轮，带抖动与退避，远低于正常浏览频率。不建议把间隔调到 1 分钟以内。

## 许可

[MIT](LICENSE)

图标版权归学堂在线所有，本项目仅作识别用途，后续将替换为自有设计。本项目与学堂在线、雨课堂官方无关联，请自行评估使用风险。
