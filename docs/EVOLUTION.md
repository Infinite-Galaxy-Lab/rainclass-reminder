# 维护与迭代手册（EVOLUTION）

> 本项目的维护文档：现状、改动记录、代码地图与待办。
> 目标读者：接手这个项目的开发者。
>
> 阅读顺序：
> 1. 本文（现状 + 改动记录 + 待办）
> 2. [ARCHITECTURE.md](ARCHITECTURE.md)（分层与数据流）
> 3. [API_NOTES.md](API_NOTES.md)（雨课堂接口真实结构）
>
> **最后更新**：2026-11-08 · 版本 0.6.6 · 状态：可用（真实账号验证通过）

---

## 一、项目速览

| 项目 | 值 |
|---|---|
| 用途 | 长江雨课堂作业/考试/讨论截止前多渠道提醒 |
| 语言 | Go 1.23（纯 Go，无 cgo） |
| 代码量 | 8,612 行 Go + 5,086 行测试 + 1,784 行前端，163 个测试函数 |
| 二进制 | 13 MB（windows/amd64，`-s -w`），6 平台交叉编译 |
| 常驻内存 | 25–35 MB |
| 直接依赖 | yaml.v3 / modernc.org/sqlite / qrterminal / beeep |
| 数据源 | `https://changjiang.yuketang.cn`（只读 GET） |
| 登录方式 | 微信扫码 OAuth / 浏览器 cookie 导入 |
| 推送通道 | 飞书 / 桌面通知 / 通用 Webhook / SMTP 邮件 |

### 目录职责（改代码前先看这里）

```
cmd/ykt-reminder/main.go      入口，只调 cli.Run
internal/cli/                 子命令：run/tasks/auth/notify/probe/web
internal/config/              配置加载 + 平台路径 + 校验
internal/domain/              ★纯领域层：Task/Milestone/Policy，零 I/O
internal/ykt/                 ★唯一知道雨课堂 API 的层
  client.go                   共享 http.Client、cookie jar、重定向策略
  endpoints.go                所有 URL 常量（改接口只改这里）
  api.go                      各接口调用 + FetchAll 编排
  dto.go                      接口数据结构（宽容类型）
  mapper.go                   DTO → domain.Task（类型判定在这里）
  flex.go                     FlexInt/FlexString/FlexTime 宽容解析
  errors.go                   ErrAuthExpired/ErrRateLimited 等分类
internal/auth/                登录态：cookie 存储 + 微信扫码
internal/store/               SQLite：去重表、任务快照、KV
internal/notify/              Notifier 接口 + 飞书 + 文案渲染
internal/scheduler/           编排：抓取→决策→发送→记账 + bootstrap
internal/netguard/            SSRF 防护（仅飞书域名白名单）
internal/qrutil/              二维码终端渲染 + JPEG→PNG 归一
internal/webui/               本地面板（embed 静态资源）
```

### 依赖方向（不可违反）

```
cmd/ykt-reminder → cli
cli              → {auth, config, domain, notify, qrutil, scheduler, store, webui, ykt}
scheduler        → {domain, notify, store, ykt}
webui            → {auth, config, notify, qrutil, store, ykt}
notify           → {domain, netguard}
ykt              → {domain}
store            → {domain}
auth             → 仅标准库
config           → 仅标准库 + yaml
domain           → 仅标准库   ★ 零内部依赖，这是分层的基石 ★
netguard         → 仅标准库
```

改代码时若发现需要反向 import，说明分层设计需要调整，而不是加 import。

**已知的设计妥协（2026-11-08 审计结论，均为有意保留）**：

| 现象 | 为什么不改 |
|---|---|
| `scheduler` import 了 `ykt`（`source.go` 的 `YktSource`） | 适配器放这里让 `cli.run` 更薄。严格做法是上移到 `cli`，但收益只是"注释与代码一致"，改动面更大 |
| `webui` 依赖 `*ykt.Client` / `*auth.Store` 具体类型 | 它是第二个装配/展示层，不是纯领域服务。只有 `repo store.Repository` 走接口。再抽接口会让 400 行变 700 行样板 |
| `store.Repository` 接口定义在实现方而非使用方 | 与 `Fetcher`（定义在使用方）风格不一致，但只有一个实现、测试用内存库，无实际代价 |

**判断标准**：新增依赖前问一句"这个包会因此知道它不该知道的事吗"。
`domain` 保持零依赖是硬底线，其余位置允许务实。

### CGO：100% 纯 Go（有守门检查）

```
grep -rn 'import "C"'  → 无
grep -rn '#cgo'        → 无
grep -rn '//go:build'  → 无（不存在某平台走 cgo 的分支）
```

三平台依赖闭包均不含 `runtime/cgo`。逐个确认：

| 依赖 | 结论 |
|---|---|
| `modernc.org/sqlite` | 纯 Go，基于 `modernc.org/libc`（libc 的 Go 转译实现） |
| `gen2brain/beeep` | 三平台纯 Go：Windows 走 WinRT Toast + COM（syscall）、Linux 走 D-Bus、macOS 用 `os/exec` 调 osascript |
| `mdp/qrterminal/v3` | 纯 Go（终端字符画渲染） |
| `gopkg.in/yaml.v3` | 纯 Go |

**守门检查**：`deploy/build.sh` 在编译前执行

```bash
if go list -deps ./... | grep -q "runtime/cgo"; then
  echo "错误：检测到 runtime/cgo" >&2; exit 1
fi
```

配合 `export CGO_ENABLED=0` 与 `Dockerfile` 的 `RUN CGO_ENABLED=0 GOOS=linux go build`，
将来任何引入 cgo 的依赖都会在打包时立即失败，而不是悄悄产出动态链接的二进制。

注：`godbus/dbus/v5` 确有两个 cgo 文件，但受文件名约束只在 dragonfly/freebsd 编译，
不影响 windows/linux/darwin 三目标（已用 `GOOS=... go list -deps` 实证）。

**依赖精简**：`skip2/go-qrcode` 从未被引用，已 `go mod tidy` 移除。
直接依赖从 5 个降到 4 个。

---

## 二、如何获取飞书 Webhook（逐步说明）

> **⚠️ 先分清两件事**：
>
> | | 群机器人（本项目需要） | 开放平台应用（**不是**本项目需要） |
> |---|---|---|
> | 入口 | 飞书客户端 → 群聊 → 设置 | 飞书开放平台 open.feishu.cn |
> | 凭据 | 一个 Webhook URL | App ID + App Secret |
> | 用法 | 直接 POST 即可 | 需换 tenant_access_token、申请权限、发布版本 |
>
> 如果你在「飞书开放平台」里创建了应用并拿到 App ID/Secret，**那不是本项目要的东西**，
> 而且 App Secret 一旦泄露应立刻在开放平台重置。本项目只需群机器人 Webhook。

### 前置条件

必须是一个**群聊**（单聊不支持自定义机器人）。自己建一个只有自己的群即可。

### 步骤

1. **打开飞书客户端**（PC 或手机均可），进入目标群聊。
   **注意**：是在飞书的消息界面里，不是在「飞书开放平台」开发者后台。

2. **进入群设置**
   - PC：点击右上角 `···` → 选择「设置」
   - 手机：点击右上角 `···` → 「群设置」

3. **找到机器人入口**
   在设置里选择「**群机器人**」（部分版本叫「机器人」）。

4. **添加机器人**
   点击「**添加机器人**」→ 在列表中选择「**自定义机器人**」（Custom Bot）。

5. **填写名称与描述**
   例如名称填 `雨课堂提醒`，描述可留空。点击「添加」。

6. **复制 Webhook 地址**
   添加成功后会弹出配置页，其中「**Webhook 地址**」形如：

   ```
   https://open.feishu.cn/open-apis/bot/v2/hook/xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
   ```

   点击复制。**这个地址就是全部凭据，请当作密码对待。**

7. **（可选但推荐）开启签名校验**
   在同一配置页往下找「**安全设置**」，勾选「**签名校验**」，会生成一个
   `secret`（形如 `xxxxxxxxxxxxxxxxxxxx`）。
   开启后请求必须带签名，否则会被拒绝——本程序已完整支持，
   把 secret 一起填到配置即可。

   其他安全选项（IP 白名单、关键词）**不要开启**，否则会拦掉本程序的请求。

8. **完成**
   点击「完成」。机器人会出现在群成员列表里。

### 如果找不到「自定义机器人」（企业限制）

部分学校/企业的飞书管理员会禁用群自定义机器人。表现是「群机器人」里
只有官方机器人，没有「添加机器人」按钮，或添加列表里没有「自定义机器人」。

**排查顺序**：

1. 确认是**群聊**而非单聊（单聊没有群机器人入口）
2. 确认自己是群主或群管理员（普通成员可能无权限）
3. 换一个自己新建的群试（有些老群沿用了企业策略）
4. 升级飞书客户端到最新版（老版本入口位置不同）
5. 若确实是管理员禁用，**不要尝试绕过**——改用下面的替代通道

### 替代通道（无飞书也能用）

本程序支持可插拔通道，飞书不可用时有四个选择：

| 通道 | 说明 | 需要什么 |
|---|---|---|
| **desktop** | 本机 Windows/macOS/Linux 原生通知 | 什么都不需要，开箱即用 |
| **email** | SMTP 邮件，适合留档与检索 | 邮箱的 SMTP 地址 + 授权码 |
| **webhook** | 通用 HTTP 推送，可对接 Bark/Server酱/PushPlus/企微/钉钉/Telegram/Discord | 对应服务的一个 URL |
| feishu | 飞书群机器人 | webhook URL |

#### email 通道（v0.6.4 新增）

```yaml
notify:
  enabled: [feishu, email]
  email:
    host: "smtp.qq.com"     # SMTP 服务器
    port: 465               # 465 隐式 TLS（推荐）/ 587 STARTTLS
    username: "you@qq.com"  # 完整邮箱地址
    password: ""            # 环境变量 YKT_EMAIL_PASSWORD（授权码，不是登录密码）
    from: ""                # 留空 = username
    to: ["you@qq.com"]
    subject_prefix: "【雨课堂提醒】"
```

常见邮箱的 SMTP 参数：

| 邮箱 | 服务器 | 端口 | 密码类型 |
|---|---|---|---|
| QQ | `smtp.qq.com` | 465 | 设置里开启 SMTP 后生成的**授权码** |
| 163 | `smtp.163.com` | 465 | 同上，授权码 |
| Gmail | `smtp.gmail.com` | 465 | 应用专用密码（需开两步验证） |
| 校园邮箱 | `smtp.<学校域名>` | 465 或 587 | 多数为登录密码，少数需单独开通 |

**安全设计（已实测验证）**：

- `465` 建连即 TLS 握手；其它端口**强制 STARTTLS**，服务端不支持就直接失败
  ——绝不降级到明文，否则授权码会明文过网
- 每次发信前用 `netguard` 重新解析主机并逐 IP 校验，拒绝内网/回环地址
  （防 SSRF 与 DNS 重绑定）
- 构造阶段**不做 DNS 解析**：否则离线或 DNS 抖动会导致程序起不来
- 正文里的任务标题来自雨课堂接口，HTML 段已全部转义；
  `javascript:` 类链接会被过滤成 `#`
- 邮件是 `multipart/alternative`：纯文本段 + HTML 段，
  纯文本能提升投递率（不被当成营销邮件）

> 测试用了一个最小 SMTP 服务端（`email_test.go` 里的 `fakeSMTP`），
> 覆盖"服务端不支持 STARTTLS 时必须拒绝且不泄露凭据"这条底线。
> 反向验证过：去掉检查后测试会变红（错误信息从 STARTTLS 变成
> "unencrypted connection"——标准库 `smtp.PlainAuth` 还有第二道保险）。

配置示例（只用桌面 + Bark 推手机）：

```yaml
notify:
  enabled: [desktop, webhook]
  desktop:
    app_name: "雨课堂提醒"
  webhook:
    name: "bark"
    url: ""          # 环境变量 YKT_WEBHOOK_URL 注入，不要写进文件
    format: "json"
```

```bash
export YKT_WEBHOOK_URL="https://api.day.app/你的key"
```

**各服务 URL 形态速查**：

| 服务 | URL 形态 | 备注 |
|---|---|---|
| Bark（iOS） | `https://api.day.app/{key}` | 最简单，装 App 即得 key |
| Server酱（微信） | `https://sctapi.ftqq.com/{key}.send` | 免费额度够个人用 |
| PushPlus（微信） | `https://www.pushplus.plus/send` | token 走查询串 |
| 企业微信群机器人 | `https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key={key}` | 需在企业微信群里添加 |
| 钉钉群机器人 | `https://oapi.dingtalk.com/robot/send?access_token={token}` | 可能需加签 |
| Telegram | `https://api.telegram.org/bot{token}/sendMessage` | 需 chat_id 参数 |
| Discord | `https://discord.com/api/webhooks/{id}/{token}` | 直接可用 |

通用通道发送的 JSON 形如：

```json
{
  "title": "🚨 作业 即将截止：第三章习题",
  "body": "课程：工程数学(2)\n截止：11-13 23:59\n...",
  "content": "（同上，标题+正文）",
  "text": "（同上）"
}
```

同时带四个字段别名，是为了兼容不同服务的字段要求。若某服务要求
特定字段名，用 `title_field` / `body_field` 覆盖。

### 配置到程序里的三种方式

**方式 A：环境变量（推荐，密钥不落盘）**

```bash
# Windows PowerShell
$env:YKT_FEISHU_WEBHOOK = "https://open.feishu.cn/open-apis/bot/v2/hook/xxx"
$env:YKT_FEISHU_SECRET  = "签名密钥（没开签名校验就不用设）"

# Linux / macOS
export YKT_FEISHU_WEBHOOK="https://open.feishu.cn/open-apis/bot/v2/hook/xxx"
export YKT_FEISHU_SECRET="签名密钥"
```

**方式 B：配置文件**

编辑 `config.yaml`（Windows 在 `%AppData%\ykt-reminder\config.yaml`）：

```yaml
notify:
  enabled: [feishu]
  feishu:
    webhook: "https://open.feishu.cn/open-apis/bot/v2/hook/xxx"
    secret: ""
    at_user_id: ""
```

**方式 C：Docker / systemd**

- Docker：写在 `deploy/docker-compose.yml` 的 `environment:` 段，或用 `.env` 文件
- systemd：写在 `/etc/ykt-reminder/env`（权限 600），unit 已配 `EnvironmentFile`

### 验证

```bash
./ykt-reminder notify test          # 手机应收到一张卡片
./ykt-reminder notify test --dry-run # 只打印内容，不发送
```

### 常见错误对照

| 现象 | 原因 | 解决 |
|---|---|---|
| `飞书 webhook 校验失败: host "xxx" 不在白名单内` | 域名不是 `open.feishu.cn` / `open.larksuite.com` | 检查是否复制了完整地址；国际版用 larksuite 域名 |
| `飞书拒绝: code=19021 msg=sign match fail` | 开启了签名校验但 secret 没配或配错 | 补上 `secret`，或用环境变量 `YKT_FEISHU_SECRET` |
| `飞书拒绝: code=19024` | 群机器人被移除或 webhook 已重置 | 重新添加机器人，换新 webhook |
| `飞书拒绝: code=19001` | 消息体格式问题 | 通常是代码改动导致，检查 `render.go` |
| HTTP 403 | 开了 IP 白名单 | 关闭 IP 白名单 |
| 收不到但无报错 | 开了「关键词」限制 | 关闭关键词，或把关键词加到通知标题里 |

**安全提醒**：webhook 泄露等于任何人都能往你群里发消息。
不要提交进 git（`.gitignore` 已排除 `data/`，但配置文件在用户目录不受管），
不要贴在公开 issue 里。泄露后在飞书里删除机器人并重新添加即可作废。

### 实测记录（2026-11-07）

- 机器人创建成功于群聊，开启「签名校验」（未开关键词、未开 IP 白名单）
- `notify test` → `通道 feishu 发送成功`，手机飞书收到卡片
- 凭据存放：`HKLM/HKCU` 用户级环境变量 `YKT_FEISHU_WEBHOOK` / `YKT_FEISHU_SECRET`
  （`setx` 写入，**新进程才生效**；已在运行的常驻进程需重启）
- 仓库扫描确认：`*.go` / `*.md` / `*.yaml` / `*.json` 内无真实 webhook 与 secret，
  仅测试文件里有 `.../hook/abc` 这类假值

---

## 二·五、公网可用性说明（重要）

一句话答案：**提醒功能在公网可用，面板默认不对外。**

| 能力 | 公网是否需要 | 现状 |
|---|---|---|
| 拉取雨课堂数据 | 服务器出网即可，**不需要公网 IP** | ✅ 任何能上网的机器都能跑 |
| 飞书/desktop/webhook 推送 | **不需要公网 IP**（主动出站 HTTPS） | ✅ 已完成 |
| 本地面板 `127.0.0.1:8787` | 不需要 | ✅ 默认只监听回环，公网访问不到 |
| 手机在外面看面板 | 需要公网暴露 | ⚠️ 未做，见下 |

**为什么推送不需要公网**：飞书 webhook 是**出站** HTTPS 请求，程序主动连
`open.feishu.cn:443`。家用宽带 NAT 后面、没有端口映射也能正常推送。
所以「在 NAS/云服务器/自己电脑上跑」都能收提醒。

**面板要公网访问时的三条路**（按推荐度）：

1. **不暴露**（推荐）：桌面远程/向日葵看一眼，或干脆只看飞书推送
2. **走隧道**：`cloudflared tunnel` / `tailscale serve` / `frp`，
   面板继续绑 `127.0.0.1`，隧道在前面加 TLS 与鉴权
3. **直接绑公网**：必须同时满足
   - `web_ui.listen: 0.0.0.0:8787`（`New()` 会强制要求非空 token，否则拒绝启动）
   - `web_ui.token: <长随机串>`（32+ 位），访问带 `?token=`
   - 前面套 nginx/caddy 做 **HTTPS**（否则 token 明文过网）
   - 云安全组只放行必要端口

**已知缺口（若要真上公网应先补）**：

- 面板无登录会话、无频率限制、无 CSRF 防护 —— token 目前即是唯一凭据
- token 走查询串，会进服务器访问日志与浏览器历史
- 建议补：`Authorization: Bearer` 优先、失败次数限流、`X-Frame-Options` 等响应头

**只读性带来的好处**：即便面板或 cookie 泄露，攻击面也只是「读取你的作业列表」，
因为 `internal/ykt` 里没有任何提交/答题方法（见 §七「绝对不要做的事」）。

---

## 三、改动记录（CHANGELOG）

### v0.6.6 — 2026-11-08 · 详情接口从没成功过（三层静默失效）+ 路由彻底解耦 + 浮点时间戳

本轮从一条**面板现象**出发：464 个任务的 `status` **全部是 `unknown`**。
顺着这个现象查下去，挖出一条连续的根因链——每一层都返回 **HTTP 200**，
所以谁也没发现。

**根因链（四层，缺一层都解释不了现象）**

| # | 层 | 错误 | 后果 |
|---|---|---|---|
| 25a | 参数 | `leaf_info` 传的是 `leafinfo_id` | 40/40 样本返回 `404000 Objects does not exist.` |
| 25b | 请求头 | 没有 `classroom-id` | 返回 `40000 CLASSROOM ID IS REQUIRED` |
| 27 | 错误码字段 | `APIEnvelope` 只认 `errcode`/`code`，真实字段是 `error_code` | **两个都读成 0 → `OK()` 判成功** |
| 25c | DTO 字段名 | 读 `is_finish`/`status`/`score_status`/`exam_id`，真实响应**没有这些字段** | 完成状态永远是零值 |

关键证据：用 `leafinfo_id` 请求 40 个样本 **0 成功**；改传**章节树节点 id**
并带 `classroom-id` 头，26 个样本 **25 成功**（唯一失败是已删除对象）。

> 这是 bug #23 的同一个错误又犯了一次。v0.6.5 修链接时已经知道
> "网页路由要节点 id，不是 leafinfo_id"，但**没顺着查详情接口**。

**修复**

- `leafToCandidate`：`leafInfoID` 不再参与任何请求，`leaf_infoCached` 改传 `leaf.leafID`
- `getJSONScoped`：新增带 `classroom-id` 头的请求路径
- `APIEnvelope`：新增 `ErrorCode`（`error_code`）+ `Success` 改 `FlexBool`
  + `successSet` 区分"明确 false"与"字段缺失"
- `LeafInfoData`：删掉四个臆测字段，改为实测存在的
  `name` / `force_reason` / `is_locked` / `is_deleted` / `is_assessed` / `content_info`
- **移除 `class_end_time` 兜底**：实测《第一章习题》`score_deadline=2026-10-15`
  而 `class_end_time=2027-01-31`，拿后者兜底会让已截止作业显示"还有 84 天"

**完成状态改由学习日志提供**

详情接口**没有任何**完成状态字段。学习日志的 `status` 才是唯一来源。
实测 65 条作业/考试逐条比对 `score` 字段：

| status | score | 条数 | 判定 |
|---|---|---|---|
| 3 | 有值（含 `0/4` 这种低分） | 48 | 已完成 |
| 1 | 空 | 5 | 未完成 |
| 0 | 空 | 12 | 未完成 |

> 3 是"已完成"不是因为分数高，而是**服务端给出了成绩**。
> `0/4` 考了 0 分也算完成——这条必须锁在测试里，否则会被人改成"看分数判断"。

新增 `OptInt` 类型：`status=0` 是合法取值，必须与"字段缺失"区分开。
新增 `FlexBool`：`is_locked` 等是 JSON 布尔，`FlexInt` 会把 `true/false` 都读成 0。

**路由与业务类型彻底解耦（本轮最大的结构性修正）**

用户提供的第三条真实链接推翻了这个设计：

```
/ai-workspace/lms-graph/25570088/forum/48057205?is_chapter=1&node_id=6338135
```

两处关键事实：
1. 「小组作业——高斯分布的应用」的 `leaf_type=4`——**讨论**，不是作业
2. `node_id=6338135` 是叶子的 **`chapter_id`**，不是它自己的 `node_id` 字段（2308919）

更要紧的是：`leaf_type` **在同一门课内都会漂移**——

| leaf_type | 出现的标题 | 服务端标签 |
|---|---|---|
| 6 | 讨论-导论 | 讨论 |
| 0 | 课前讨论：古典概型 | 讨论 |
| 4 | 小组作业——高斯分布的应用 | 讨论 |
| 7 | P1.3 电路元件 / 作业1单元 / 期中2026 | 雨课堂课件（全部） |

于是把两件事拆开：

- **业务类型**（`domain.TaskType`）：展示标签 + 是否参与截止提醒
- **路由**（`routeKind` + segment）：网页端把内容挂在哪个组件下

路由判定**优先用服务端标签** `/leaf_info` 的
`content_info.score_evaluation.name`，它比 `leaf_type` 和标题都可靠；
拿不到详情时才退回 `leaf_type`。

**服务端标签 → 路由（真机逐条打开验证）**

| 服务端标签 | 路由 | 反例（走错会怎样） |
|---|---|---|
| 讨论 | `forum` + `node_id=chapter_id` | 走 quiz 报「没有权限」 |
| 作业 / 考试 | `quiz`（可被学习日志升级为老版页） | — |
| 视频 / 图文 / 课堂 / 雨课堂课件 | `video` | 走 quiz 报「没有权限」；走 node 报「知识点不存在」；走 exercise 报「页面不存在」 |

> 图文(3) 与课件(7) 走 `video` 路由这个结论反直觉，但真机验证就是这样：
> 它们是同一个"内容阅读器"组件，只有 `/video/{id}` 能渲染正文与讨论区。

**真机验证（8 个样本 × 6 类路由，全部通过）**

| 路由 | 样本 | 页面实际渲染 |
|---|---|---|
| exam | 半期考试 / 课堂测试1 | ✅ 考生须知、试卷得分、已交卷 |
| forum | 讨论-导论 / 讨论-第1章 | ✅ 开放型作业、讨论区 |
| quiz | A卷期末考试 / 国家安全教育测试 | ✅ 95/100 已交卷（内嵌 exam 页） |
| quizSummary | T1.1 电路及其基本物理量 | ✅ 满分5分/4题/逐题班级正确率 |
| video | P1.3 电路元件 / 复习预习：指针与引用 | ✅ 完成度0%、讨论区、考核截止时间 |

三条用户给出的基准链接**逐字一致**：

```
✅ exam    /v2/web/exam/24984528/2051258
✅ quiz    /v2/web/quizSummary/25570085/1745475
✅ forum   /ai-workspace/lms-graph/25570088/forum/48057205?is_chapter=1&node_id=6338135
```

**数据修复前后对比**

| 指标 | v0.6.5 | v0.6.6 |
|---|---|---|
| 完成状态 unknown | **464（全部）** | 374 |
| 已完成 done | 0 | **64** |
| 未完成 pending | 0 | **26** |
| 路由种类 | 3 | **6**（exam/quizSummary/forum/quiz/video/node） |
| discuss 正确走 forum | 0 | 27 |

**测试**：新增 `detail_p0_test.go`（5 个）+ `routing_test.go`（5 个），
全部做过**反证验证**（故意改错映射，确认测试真的变红）。
另修正 6 处旧断言——它们锁的是 v0.6.5 甚至更早的错误假设，
包括 `TestDetailRefillKeepsNodeID` 里那句"详情接口应使用 leafinfo_id（这是对的，别一起改错）"。

> 教训（第二次同类）：v0.6.5 修链接时已经确认"节点 id 与 leafinfo_id 是两个
> 不同的东西"，却只改了链接、没去查同样是"按 ID 查详情"的那个接口。
> **已知一个 ID 混用错误时，应该把所有用这个 ID 的地方都过一遍。**
>
> 教训（结构性）：一个"类型"字段同时承担"展示"和"路由"两个职责时，
> 任何一方的修正都会崩掉另一方。本轮四次修正（考试、讨论、课件、图文）
> 里有三次都是这个耦合造成的。解耦之后才停下来。

**补充：bug #28 —— 浮点毫秒时间戳解析失败（同日发现）**

排查"计分课件没被提升为作业"时，日志打出了决定性的证据：

```
msg="leaf7 详情结果" label=雨课堂课件 assessed=true sd=0 type_after=video
```

`sd=0` 与接口原始响应矛盾——直接请求同一叶子返回
`"score_deadline": 1778428740000.0`。**带小数点**。

根因：`FlexTime.UnmarshalJSON` 只用 `strconv.ParseInt`，而
`ParseInt("1778428740000.0")` 失败；失败后又按"解析失败返回 0"的宽容策略
静默退化。于是：

- 详情给出的截止时间被丢成 0
- `is_assessed && score_deadline > 0` 判据永远不成立
- 33 个计分课件停留在 `TaskVideo`，**完全不参与截止提醒**

修复：`ParseInt` 失败后追加 `ParseFloat` 分支。

| 指标 | 修复前 | 修复后 |
|---|---|---|
| video 类型 | 325 | **292** |
| homework 类型 | 57 | **90** |
| 有截止时间却标 video | 34 | **1** |

剩下那 1 条是真视频（`is_assessed=false`、`sd=0`、标签「视频」），
其截止时间来自章节树，本就不该提醒，属正确行为。

> 教训：这里的"宽容解析"（解析失败返回 0 而不报错）本来是防崩溃的设计，
> 但它把**类型不匹配**伪装成了**数据缺失**。宽容解析应当配一条
> "值非空但解析结果为零"的告警，否则错误永远静默。

### v0.6.5 — 2026-11-08 · 试卷链接真正修好（考试/习题走学习日志取 ID）

用户给了两条**能正常打开的真实链接**，直接推翻了 v0.6.3 的结论：

```
考试  /v2/web/exam/24984528/2051258
习题  /v2/web/quizSummary/25570085/1745475
```

v0.6.3 我以为"所有任务都走 ai-workspace"，错了。真相是**三类任务三种 ID 来源**。

**排查过程（记录推理链，避免重走弯路）**

1. 先假设真实 ID 是 `exam.exam_id` → 实测**全站 464 个叶子的 exam.exam_id 全为空**，假设推翻
2. 再查 `leaf_info` 详情 → 对这两类叶子返回 `404 Objects does not exist.`，也没有
3. 在浏览器里打开真实考试页抓包 → 发现它请求
   `/v/exam/cover?exam_id=2051258`，说明 2051258 **确实叫 exam_id**，但来源不明
4. 逐个接口搜索 `2051258` 这个字面值 → 命中
   `/v2/api/web/logs/learn/{cid}?actype=-1&page=0&offset=20&sort=-1`
   的 `data.activities[].courseware_id`
5. 用同样方法验证习题案例 → 同一接口，`type=4`，`courseware_id=1745475`

**根因**：章节树只给 node id / leafinfo_id，**试卷 ID（courseware_id）只存在于学习日志**。
两者之间没有任何可 join 的字段，只能用**标题精确匹配**做关联。

**三种路由与 ID 来源**

| 任务类型 | 路由 | ID 来源 |
|---|---|---|
| 考试 | `/v2/web/exam/{cid}/{courseware_id}` | 学习日志 `type=5` |
| 习题/测验 | `/v2/web/quizSummary/{cid}/{courseware_id}` | 学习日志 `type=4` |
| 视频/讨论 | `/ai-workspace/lms-graph/{cid}/{video\|forum}/{node_id}` | 章节树 node id |

**实现要点**

- 新增 `LearnLogs` 接口（`pathLearnLogs`），分页拉取，`actype=-1`
  （**`actype=100` 返回空列表，踩过**），6 小时 TTL 缓存
- `coursewareIndex` 按归一化标题建索引；只收 `type=4/5`
- **开销控制**：只有章节树里确实存在考试/习题叶子时才拉日志
  → 实测日志请求从 **33 次降到 14 次**
- 匹配不上时静默退回 ai-workspace 路由，不影响任务本身

**真机验证（真实账号 + 真实浏览器）**

| 任务 | 生成链接 | 页面实际内容 |
|---|---|---|
| 模拟题1 | `/v2/web/exam/24984528/2051258` | ✅ 100 分 / 50 题 / 截止 2026-12-31 / 继续答题 |
| T1.1.4 PN结 | `/v2/web/quizSummary/25570085/1745475` | ✅ 5 分 / 4 题 / 逐题答案与班级正确率 |

最终链接分布：考试页 52 条、习题成绩页 38 条、ai-workspace 374 条。

**测试**：新增 5 个用例（真实案例对照、type 过滤、路由映射、标题归一化、分页+缓存），
并更新 4 处旧断言——它们此前锁的是 v0.6.3 的错误假设。

> 教训：v0.6.3 我"真机验证通过"了 3 个类型就下了"全部走 ai-workspace"的结论。
> 实际上我只测了**习题/讨论/视频**，考试和已经做过的习题根本没测到。
> 样本覆盖不全时的结论，不能写成通则。

### v0.6.4 — 2026-11-08 · 邮件通道 + 多账号方案定案

**1. 新增 email（SMTP）通道**

`internal/notify/email.go`（约 430 行）+ 配置节 `notify.email` +
环境变量 `YKT_EMAIL_PASSWORD`。

- 465 隐式 TLS / 其它端口强制 STARTTLS，**不支持就失败**（不明文传授权码）
- 每次发信前重新解析主机并逐 IP 校验（复用 netguard）
- 构造阶段不做 DNS 解析——否则离线会让程序起不来
- `multipart/alternative`（纯文本 + HTML），标题 RFC 2045 编码
- HTML 段转义任务标题（外部输入），过滤 `javascript:` 链接
- base64 手工按 76 列折行（标准库编码器不分行，严格服务器会拒收）

测试 10 个用例，含一个自制 SMTP 服务端，覆盖"拒绝明文认证且不泄露凭据"。

**2. 多账号：评估后改用多实例**

子代理逐个文件核对后，结论是单进程多账号要动 8 个包、15-17 个文件、
40-60 处代码，且 `pushes.key` 是全局主键——不改 schema 的话第二个账号的
提醒会被**静默吞掉**，用户毫无感知。

改用多实例（`data_dir` + `web_ui.listen` + `--config` 本来就都支持）。
**实测验证**：两个实例分别跑在 8787 / 8799，各自独立的 cookie、
SQLite、日志，互不可见，一个已登录 464 任务、另一个未登录 0 任务。
模板见 `configs/config.second-account.example.yaml`。

### v0.6.3 — 2026-11-08 · 直达链接彻底修正（真机验证通过）

用户在浏览器里登录后提供了可正常打开的真实页面
`/ai-workspace/lms-graph/25570088/quiz/48057249?is_chapter=1`，
这一条 URL 直接定位了两个 bug。

**修 2 个缺陷**

| # | 现象 | 根因 | 修复 |
|---|---|---|---|
| 23 | 点任务打开「此内容已被老师删除」 | 链接传 `leafinfo_id`（48059470），网页端要节点 `id`（48057249）；且**两条生成路径不一致**——`toTask` 修了、详情回填没修 | 路由全部改为 `/ai-workspace/lms-graph/:cid/{quiz\|video\|forum\|node}/:node_id?is_chapter=1`；`api.go` 回填处改用 `leaf.leafID` |
| 24 | 视频任务点开**右侧内容空白**；且真实视频叶子根本没被采集 | 章节树是三层：`section_leaf_list[].leaf_list[]`。DTO 没有 `LeafList` 字段，把"节"（`leaf_type=0`、无 `leafinfo_id`）当成了任务 | `LeafNode.LeafList` + `expandLeafNode()`：有 `leaf_list` 就下钻，节本身不入任务；空容器直接跳过 |

**直接效果**：任务总数 **363 → 464**（+101 个被漏掉的真实视频叶子）。

**真机验证**（真实账号 + 真实浏览器，逐类型）

| 类型 | 结果 |
|---|---|
| 习题 `quiz/48057249?is_chapter=1` | ✅ 渲染出《第三章习题》试卷（100 分 / 26 题 / 继续答题按钮） |
| 讨论 `forum/41413523?is_chapter=1` | ✅ 图文材料 + 讨论区（18 条） |
| 视频 `video/48057171?is_chapter=1` | ✅ 播放器加载（11:03）+ 知识引导 + 讨论区 |
| 旧写法 `quiz/48059470`（leafinfo_id） | ❌ 「此内容已被老师删除」 |

**路由来源**：`/fe/static/web/1.2.352/js/aiworkspace.*.js` 里的
`new Router({base:"/ai-workspace", mode:"history"})` 及 `/lms-graph/:classroom_id`
的 children 数组。完整子路由表见 §4.3.1。

**测试**：新增 `TestNestedLeafListExpanded`、`TestEmptySectionSkipped`、
`TestDetailRefillKeepsNodeID`、`TestTaskURLUsesNodeIDNotLeafInfoID`，
全部做过反向验证（把 bug 放回去确认测试变红）。

> 反面教训：`TestNestedLeafListExpanded` 第一次写"反向验证"时我把
> `expandLeafNode` 改坏了语法，导致编译失败而不是测试失败——**编译失败
> 不算验证成功**，必须看到断言真的红了。

### v0.6.2 — 2026-11-08 · 路由换成 ai-workspace（未上线，合并进 v0.6.3）

### v0.6.1 — 2026-11-08 · 修正"续期"时间戳的语义

v0.6.0 让 `RefreshedAt` 只在 cookie **值变化**时推进。实测发现这个判断是错的：
`csrftoken` 在同一会话内连打 3 次完全一致（指纹相同），于是时间戳会
永远冻在登录那一刻，面板上看起来像"很久没续期"，反而误导用户。

改为：只要服务端下发了任何 cookie 就推进时间戳（内存值）；值没变时
不立即写盘，只标 dirty 等 `Flush` 补写。这样面板上的"续期"时间是
**活性证据**——它在动，就说明请求真的通过服务端校验了。

配套测试 `TestObserveCookieRefreshedAtAdvancesWithoutChange`。
写这个测试时踩到：`time.Time` 是结构体，`json:"…,omitempty"` **对结构体无效**，
零值照样会序列化成 `"0001-01-01T00:00:00Z"`。所以不能用"文件里有没有
这个键"判断是否写盘，必须比较值。

### v0.6.0 — 2026-11-08 · 登录态实测与加固

**背景**：用户问"为什么官方网页端/App 的登录能撑很久，我们的很快就过期"。
这一轮先做实测，再改代码。实测结论见 §4.7.1——**推翻了原先的猜测**。

**1. 实测：雨课堂不续期 sessionid**

逐个端点探测响应头，只有 `/v/course_meta/user_info` 下发 Set-Cookie
（`csrftoken` + `django_language`），且从不下发 `sessionid`。
因此"Set-Cookie 自动回写延长会话"这条路在技术上不成立。

**2. 修 3 个真实缺陷**

| # | 现象 | 根因 | 修复 |
|---|---|---|---|
| 20 | 请求里出现**重复的 cookie** | API client 同时挂了 cookie jar 和手工 `Cookie` 头，net/http 把 jar 里的追加到后面，产生 `sessionid=mine; csrftoken=c; sessionid=from-server` | 拆成两个 client：`http`（无 jar，业务请求）与 `authHTTP`（有 jar，仅扫码登录）。`HTTPClient()` 现在返回后者并加了注释警告 |
| 21 | cookie 回写是**死代码** | `ObserveCookie` 写好了，但抓取流程从不调用唯一会下发 Set-Cookie 的端点 | `FetchAll` 开头先打一次 `CourseMetaUserInfo`；错误吞掉（`Courses` 会给出更准确的失效错误） |
| 22 | 导入时**丢掉辅助 cookie** | `Cookie` 只有 `sessionid` + `csrftoken` 两个字段，其余全丢 | 新增 `Extra map[string]string` 完整保留；`headerValue()` 保证同名只出现一次且顺序固定 |

**3. 登录态能力增强**

- `CookieObserver` 接口：`ykt` 层每个响应（含 4xx/5xx）都把 Set-Cookie 交给 `auth.Store`
- 落盘 30 秒节流（`DefaultMinWriteGap`），内存值始终最新；`Flush()` 在 `App.Close` 补写
- `UpdatedAt`（登录建立时间）与 `RefreshedAt`（最近回写时间）语义分离
- 支持三种导入格式：Cookie 头字符串 / `{"k":"v"}` / DevTools 的 `[{"name","value"}]`
- 旧版只存 `raw` 的 `cookie.json` 自动迁移到结构化字段（`Cookie.normalize`）
- 扫码登录成功后保存 **jar 里的全部 cookie**

**4. 面板显示登录态健康度**

左栏新增一行（现在是三行，见下），回答"这份登录态还值得信任吗"：

```
登录 11-08 12:01      ← cookie_updated，这次登录是什么时候做的
续期 11-08 12:48      ← cookie_refreshed_at，证明请求真的通着
3 个 cookie           ← cookie_count，少于 2 个转警示色
```

**写这一行时踩的两个坑**（记下来）：
- 单行用 `·` 连接时，侧栏 208px 会在「续期 11-08」和「12:48」之间折断，
  读起来像两个信息 → 改为每行一个语义单元 + `white-space: nowrap`
- 左栏任务数原来固定取 `s.task_count`（363），与主视图的 42 条对不上
  → 改为跟随筛选结果（`renderTasks` 里更新）

**5. 测试**

新增 18 个用例，其中两个是**反向验证过的**（先故意把 bug 放回去，确认测试会红）：
- `TestNoCookieJarOnAPIRequests`：第一次写时用同一个 `leafID` 循环两次，
  命中详情缓存导致第二个请求根本没发出，测试**假通过**。改成不同 leafID 后
  立刻复现了 `sessionid 出现 2 次`。教训：验证缓存链路的测试必须换 key。
- `TestFetchAllTouchesSession`：锁住"抓取必须触达会话端点"，否则回写会再次变成死代码。

### v0.5.0 — 2026-11-08 · 面板重构 · 死功能修复 · 架构审计

**1. 面板整体重构（视觉 + 信息架构）**

- 布局从「单列卡片堆叠」改为 **左侧窄导航 + 右侧内容区**（`--rail-w` 控制，≤860px 折叠为顶部横条）
- 新增**三档显示范围**，默认 `due`（只看有截止时间且未过期）：
  - 生产账号实测 363 条任务里 **197 条是无截止的视频/课件**，
    全列出来会把 42 条真正的待办淹掉。这是本轮最重要的产品判断
  - `open` 未完成全部 / `all` 全部
- 任务项改为**紧凑列表 + 发丝分隔线**（原先每项独立卡片，一屏只能看 6 条）；
  倒计时用「符号 + 颜色 + 字重」三重编码（`○` 普通 / `▲` 警告 / `●` 紧急 / `✓` 完成 / `✕` 过期）
- 扫码登录从页面内卡片改为**模态浮层**，不打断列表浏览；支持 Esc / 点遮罩关闭
- 空态分三种成因分别提示（无数据 / 被筛选挡住并列出具体条件 / 该范围确实为空），
  每种给出对应的下一步动作
- 筛选状态存 `sessionStorage`，键升级为 `ykt-panel-v2`（结构变了，避免读到脏数据）

**2. 修 5 个真实缺陷**

| # | 现象 | 根因 | 修复 |
|---|---|---|---|
| 15 | **点「立即刷新」毫无反应** | `handleRefresh` 往自己的缓冲通道写信号，但**全仓库无消费者** | `scheduler.Trigger()` + `cli.run` 里接一条 goroutine；前端改为轮询 `last_fetch` 变新而不是死等 2.5s |
| 16 | `notify.webhook.headers` 配了无效 | `config` 定义了字段，但 `WebhookOptions` 没有、装配时没传 | 补 `Headers` 字段并在 `Send` 里 `req.Header.Set` |
| 17 | `SetSession` 数据竞争（潜伏） | `session` 字段无锁写，`applyHeaders` 并发读 | 改 `atomic.Pointer[sessionBox]`（`atomic.Pointer` 存不了接口，故包一层） |
| 18 | 面板版本号恒显示 dev | `webui` 里写死 `"dev"`，且不能 import `cli`（会成环） | `Options.Version` 由 `cli` 注入 |
| 19 | webhook 的 User-Agent 恒为 `ykt-reminder/dev` | `build.sh` 只注入了 `cli.Version` | LDFLAGS 追加 `notify.Version` |

**3. 我自己的前端 bug（写下来免得再踩）**

`.alert` 和 `.modal` 设了 `display: flex|grid`，**优先级高于浏览器默认的
`[hidden] { display: none }`**，导致 `el.hidden = true` 完全不生效——
登录正常时依然显示「登录已失效」红条。
修法是全局加 `[hidden] { display: none !important; }`。

**4. 代码卫生**

- 移除未使用依赖 `skip2/go-qrcode`（直接依赖 5 → 4）
- 清掉三处 `var _ = ...` 占位（`cli/tasks.go`、`cli/auth.go`、`notify/guard.go`）
- 清掉 `feishu.go` 里 `if at == "all" { at = "all" }` 的空操作
- `looksLikeHTML` 去掉"任意以 `<` 开头"的兜底——它把非 JSON 的正常响应
  也当成登录页，误报 `ErrAuthExpired`。现在只认 `<!doctype` / `<html`

**5. 架构审计（结论见 §一）**

- CGO 审计：确认 100% 纯 Go，三平台依赖闭包无 `runtime/cgo`
- 分层审计：`domain` 零内部依赖成立；记录了三处有意的设计妥协
- 新增 `build.sh` 的 cgo 守门检查

### v0.4.0 — 2026-11-08 · 面板重做 · 链接修复 · 登录失效误报

**1. 登录失效被误报成「解析 JSON 失败」（真实缺陷）**

雨课堂的失败响应是另一种格式，早期实现完全没覆盖：

```json
{"code": 50000, "msg": "UNAUTHENTICATED", "data": ""}
```

三个差异叠加导致误判：

| 差异 | 旧实现的表现 |
|---|---|
| 错误码字段叫 `code` 不是 `errcode` | 认为没有错误码 → 判定成功 |
| 错误信息叫 `msg` 不是 `errmsg` | 错误描述为空 |
| `data` 是空字符串 `""` 不是对象 | 反序列化报 `cannot unmarshal string into ...` |

结果用户看到的是「上游返回异常: 解析 JSON 失败」，
而真实原因是「登录态过期，请重新登录」。**排查方向被完全带偏**。

修复：`APIEnvelope` 加 `UnmarshalJSON`，用 `RawMessage` 接住 `data`
再按需二次解析；新增 `IsAuthFailure()`（识别 `code=50000`）与
`envelopeErr()` 统一映射，所有接口方法都改调它，不会再漏。

测试：`internal/ykt/envelope_test.go` 5 个用例锁定这四种响应形态。

**2. 任务链接一直跳到主页（根因：路由模式判断错误）**

旧链接 `…/v2/web/index#/course/{id}/task` 有两个错误：
`#` 是历史遗留（网页端早已是 `base:"/v2/web", mode:"history"`），
且 **`/course/:id/task` 这个路由在打包产物里根本不存在**
（已 grep 全部 121 条 route 确认）。

真实路由（从 `pc.fc4f66b7.js` 逆向提取）：

| 类型 | 路由 | 必需参数 |
|---|---|---|
| homework | `/v2/web/iframe-exercise/{classroom}/{leafinfo_id}` | 无需 sku |
| exam | `/v2/web/exam/{classroom}/{exam_id}` | exam_id 在 `leaf.exam.exam_id` |
| discuss | `/v2/web/lms/{classroom}/forum/{leaf_id}` | — |
| video | `/v2/web/video-student/{classroom}/{leaf_id}` | — |
| class/other | 退回 `/v2/web/index` | — |

**为什么不用 `/v2/web/exercise/{cid}/{leafId}/{skuId}`**：`sku_id`
**不在章节树里**（实测叶子字段无此项），只在
`/mooc-api/v1/lms/learn/leaf_info/` 的响应里。为避免为此给
`domain.Task` 加字段、且只有拉过详情的任务才有 sku，
选用了不需要 sku 的 `iframe-exercise` 路由。

新增 `webTaskURL()` 纯函数（`mapper.go`），缺参数一律退回首页——
宁可达不到详情页，也不生成 404 链接。测试 `url_test.go` 8 个用例 + e2e 4 条断言。

**3. 面板视觉重做 + 分类筛选**

- 视觉：Linear 式克制风格。三级背景层次（`bg/surface/surface-2/3`）替代硬边框；
  单一主色 `#8a94ff` + 低饱和状态色；倒计时用 **符号+颜色+字重** 三重编码
  （`○` 普通 / `▲` 警告 / `●` 紧急 / `✓` 完成 / `✕` 过期）；
  `tabular-nums` 保证位数不跳动；深色默认 + `prefers-color-scheme: light` 切换；
  ≤640px 单列堆叠
- 筛选：类型 chip（带数量角标，角标按"除类型外其他条件"计算，切类型可预览结果量）+
  课程下拉（同名课程自动追加 `classroom_id` 后缀区分）+ 关键字搜索（150ms 防抖）+
  保留原有两个开关；状态存 `sessionStorage`；空态区分"无任务"与"筛选后为空"
- 安全：全程 `createElement` 渲染，无 `innerHTML` 拼接外部数据（标题来自接口，有 XSS 面）

### v0.3.0 — 2026-11-08 · 防风控限流 + 开机自启

**核心改动：把请求量砍到 1/9，避免被雨课堂风控**

实测基线（33 门课 / 363 个叶子，v0.2.0）：

```
每轮 = 1 次课程列表 + 33 次章节树 + 279 次叶子详情 = 313 个请求
每 10 分钟一轮 → 每小时约 1878 个请求
```

四层保护，全部实测生效：

| # | 措施 | 位置 | 效果（实测） |
|---|---|---|---|
| 1 | **全局限速 `min_request_gap`**（默认 300ms） | `ykt/client.go` `pace()` | 313 个请求被摊平到 ≥94s，不再出现 4 并发尖峰 |
| 2 | **视频类跳过详情** | `ykt/api.go` `fetchCourse()` | 视频/课件无截止概念，详情拉回来也没用。197/363 个叶子属此类 |
| 3 | **详情缓存分级 `detail_ttl_tiers`** | `ykt/client.go` `ttlFor()` | 近的 15m、24h 内 60m、更远 8h |
| 4 | **轮询分级调频 `interval_tiers`** | `scheduler.nextWait()` | 2h内→5m、12h内→10m、48h内→20m、更远→30m |

**实测降幅**：详情请求 **279 → 125**（省 55%），单轮总请求 313 → 约 159。

**为什么限速要单独做**：`concurrency` 只限制"同时几个在飞"，
4 个并发仍然会在同一毫秒打到对端网关。`pace()` 用一个
"预定位"时间线（`lastSend = now + wait`）把所有请求串成均匀节奏，
并发数只影响吞吐，不影响瞬时速率。

**新增配置项**

```yaml
poll:
  min_request_gap: 300ms    # 任意两请求最小间隔，上限 3s
  relaxed_interval: 30m     # 远期无任务时的间隔
  relax_hours: 6            # 超过这么多小时没任务 → 用 relaxed_interval
ykt:
  detail_ttl: 60m           # 叶子详情缓存
```

**统计口径修正**

`FetchStats.DetailsFetched` 现在只计**真实 HTTP 请求**，
新增 `DetailsCached` 单独计缓存命中。否则缓存生效了日志里看不出省了多少。

**开机自启（Windows）**

- `deploy/install-windows.ps1` 注册计划任务 `YktReminder`（登录后 30s 触发）
- 修了两个静默失败点：
  1. **UTF-8 无 BOM** → PowerShell 5.1 按 ANSI 读，中文注释变乱码，脚本解析失败。
     已给 .ps1 加 BOM。
  2. **VBS 启动器用 ASCII 写入** → 中文路径 `D:\code\长江雨课堂通知助手`
     被写成 `D:\code\?????????\`，静默启动失败。改用 `-Encoding Unicode`。
- 为什么用 VBS 而不是直接执行 exe：Go 编译产物是控制台程序，
  计划任务直接执行会弹黑窗口；而"不管用户是否登录"模式虽然隐藏窗口，
  却拿不到交互式桌面会话，`beeep` 桌面通知就发不出去。
  VBS 的 `WScript.Shell.Run(..., 0, False)` 两个问题都解决。
- 日志必须落盘（计划任务无控制台）：`log.file` 已指向
  `%AppData%\ykt-reminder\ykt.log`

### v0.2.0 — 2026-11-07 · 多通道与凭据安全加固

**新增通道**

- `desktop`：桌面系统通知，**零配置零凭据**，Windows/macOS/Linux 原生弹窗。
  基于 `beeep` 库（WinRT Toast / osascript / D-Bus），不使用 shell 拼接。
- `webhook`：通用 HTTP 推送，可对接 Bark / Server酱 / PushPlus /
  企业微信 / 钉钉 / Telegram / Discord。载荷同时带 `title` / `body` /
  `content` / `text` 四个别名以提高兼容性，支持 `title_field` /
  `body_field` 覆盖。
- 动机：企业飞书常禁用群自定义机器人。现在即使飞书不可用也有替代路径。

**凭据安全**

- 所有密钥支持环境变量注入：`YKT_FEISHU_WEBHOOK`、`YKT_FEISHU_SECRET`、
  `YKT_WEBHOOK_URL`。源码、示例、测试中**无任何可用凭据字面量**。
- 通用 webhook 强制 https，并在每次发送前重新执行 SSRF 校验
  （拒绝环回/私有/CGNAT/云元数据）。
- 面板 token 比较改为 `subtle.ConstantTimeCompare`。

**修复**

| # | 问题 | 根因 | 修复位置 |
|---|---|---|---|
| 13 | `--config` 放在子命令前报「未知子命令」 | 解析器只看 `args[1]` | `cli.Run` 支持两种顺序 |
| 14 | `notify test` 硬编码提示配飞书 | 新增通道后提示过时 | 改为列出已启用通道并给出通用指引 |

### v0.1.0 — 2026-11-07 · 首个可用版本

**新增**

- 完整分层实现：domain / ykt / auth / store / notify / scheduler / webui
- 微信扫码登录（`auth login`）+ 浏览器 cookie 导入（`auth import`）
- 只读 API 客户端：课程列表、章节树、叶子详情、考试封面
- 提醒策略：截止前 1天/6小时/1小时/15分钟 + 过期一次 + 课堂开课前 10 分钟
- 去重机制：键含截止时间，改期自动重新计时
- 静默时段：23:00–07:00 非紧急项顺延到 07:30
- 降噪：同任务多窗口只发最紧急一条，单轮上限 8 条
- 飞书卡片推送 + 签名校验
- SQLite 持久化：推送记录、任务快照、KV
- 本地面板：任务倒计时、扫码登录、推送历史、手动刷新
- 6 平台交叉编译 + Docker + systemd + Windows 计划任务脚本

**真实账号验证发现的缺陷（已修复，按发现顺序）**

| # | 问题 | 根因 | 修复位置 |
|---|---|---|---|
| 1 | 二维码登录失败 `not a PNG file` | 微信返回 JPEG，代码只按 PNG 解码 | `qrutil/qrcode.go` 改用 `image.Decode` 自动识别 |
| 2 | 章节树解析出 0 个叶子 | 真实结构是 `data.course_chapter[].section_leaf_list[]`，公开资料写的 `data.chapters` | `dto.go` + `mapper.go` |
| 3 | 课程列表为 0 | 真实在 `data.list`，代码读 `data.courses` | `dto.go` `CourseListData.Items()` |
| 4 | 详情接口 404 | 叶子 ID 有两个：`id`（树节点）与 `leafinfo_id`（详情用） | `mapper.go` `candidateLeaf` 分别保存 |
| 5 | 作业/讨论类型混淆 | `leaf_type=6` 同时用于「作业-第1章」和「讨论-导论」 | `mapper.go` 新增 `classify()`，按标题关键词优先 |
| 6 | 197 个课件显示「剩余 85 天」 | 视频的 `score_deadline` 是整门课结束时间 | `toTask()` 对无截止概念的类型清空 Deadline；`TaskType.HasDeadline()` 排除 video |
| 7 | 首次运行补发 620 条历史提醒 | 新库 `PushedKeys` 为空，历史里程碑全被判为「该发未发」 | `scheduler.bootstrap()` 静默 24h 以前的历史项 |
| 8 | 每轮重复提醒被抑制的窗口 | 被 coalesce 压制的里程碑未记账，下轮再次成为候选 | `RunCycle` 对被抑制项也 `Claim` + `Record(suppressed)` |
| 9 | 发送失败后永久不再重试 | `Claim` 的 `ON CONFLICT DO NOTHING` 挡住 `failed` 行 | `Claim` 增加 `DO UPDATE ... WHERE status='failed'` |
| 10 | `web --listen` 报 flag not defined | 顶层 `flag.Parse` 吞掉了子命令参数 | `cli/app.go` 改为 `extractConfigFlag()` 手动提取 |
| 11 | 面板任务标题竖排挤成窄列 | CSS `grid-template-columns: 4px 1fr auto` 但只有 2 个子元素 | `style.css` 改为 `minmax(0,1fr) auto` |
| 12 | 静默顺延到「当天 07:30」（已过去） | 顺延目标未判断是否早于 now | `policy.deferTarget()` 加 `+24h` 判断 |

**单元测试覆盖的关键行为**

- 里程碑生成、改期后键变化、跨午夜静默、紧急项穿透静默
- Coalesce 只发最紧急一条、超限压制
- Claim/Record/Release 三态与重试
- Bootstrap 静默历史但保留 24h 内
- SSRF：环回/私有/CGNAT/云元数据/域名后缀伪装
- 端到端：假服务器模拟真实结构，锁定字段名

---

## 四、关键代码地图

### 4.1 提醒决策（最核心，改这里影响所有提醒行为）

`internal/domain/policy.go` — `Decide()`

```go
// 输入：当前时刻、全量任务、已发送键集合、策略配置
// 输出：本轮应处理的提醒（含被抑制/被顺延的，供记账）
func Decide(now time.Time, tasks []Task, sent map[string]bool, cfg PolicyConfig) []Reminder
```

处理顺序：过滤已完成 → 未到点 → 已发送 → 超 lookahead → 静默顺延 → 紧急优先排序 → coalesce → 单轮上限。

**改提醒时间窗**：`DefaultMilestoneConfig()` 的 `BeforeDeadline: []int{1440, 360, 60, 15}`。
**改静默时段**：`DefaultPolicyConfig()` 的 `QuietStartHour/QuietEndHour`。

`internal/domain/milestone.go` — `MilestonesFor()`

```go
// 去重键：taskID | kind | offsetMinutes | deadline.Unix()
// 含截止时间 → 老师改期自动重新提醒
func (m Milestone) Key() string
```

### 4.2 类型判定（最容易误判，改这里影响"什么算任务"）

`internal/ykt/mapper.go` — `classify()`

```go
// 名称关键词优先，leaf_type 兜底
func classify(title string, leafType int64) domain.TaskType
```

关键词表在同一文件的 `examKeywords` / `discussKeywords` / `homeworkKeywords` / `videoKeywords`。
**新增类型识别**：往对应 slice 加词即可，例如发现"期末设计"没被识别为考试，加进 `examKeywords`。

`internal/domain/task.go` — `TaskType.HasDeadline()`

```go
// 只有 homework / exam / discuss 有截止概念
// video 刻意排除（其 score_deadline 是课程结束时间）
```

### 4.3 接口路径（接口变了只改这里）

`internal/ykt/endpoints.go`

```go
const (
    pathUserBasicInfo = "/api/v3/user/basic-info"
    pathCourseList    = "/v2/api/web/courses/list"
    pathChapter       = "/mooc-api/v1/lms/learn/course/chapter"
    pathLeafInfo      = "/mooc-api/v1/lms/learn/leaf_info/%s/%s/"
    pathExamCover     = "/v/exam/cover"
)
```

**只读约束**：此文件不得出现任何 submit / answer / gen_token 类路径。

#### 4.3.1 网页端直达链接（v0.6.5 最终版，三类三源）

**三类任务，三种 ID 来源 —— 这是本项目最反直觉的一处。**

| 任务类型 | 路由 | ID 字段 | ID 来源 |
|---|---|---|---|
| 考试 | `/v2/web/exam/{cid}/{id}` | `exam_id` | 学习日志 `courseware_id`（type=5） |
| 习题/测验 | `/v2/web/quizSummary/{cid}/{id}` | `quiz_id` | 学习日志 `courseware_id`（type=4） |
| 视频/讨论/材料 | `/ai-workspace/lms-graph/{cid}/{video\|forum\|node}/{node_id}` | `node_id` | 章节树 `id` |

**为什么不能统一**：这三类内容在雨课堂里是**三套不同的子系统**，
前端也是三个不同的 SPA 入口。强行统一只会得到打不开的链接。

**学习日志接口（唯一能拿试卷 ID 的地方）**

```
GET /v2/api/web/logs/learn/{classroom_id}?actype=-1&page=0&offset=20&sort=-1
→ {"data":{"activities":[{"type":5,"courseware_id":"2051258","title":"模拟题1:..."}]}}
```

- `actype=-1` = 全部；**`actype=100` 返回空列表**（踩过，浪费了一轮排查）
- `type` 决定路由：`4` 习题 / `5` 考试 / `9` 公告 / `14` 视频
- 分页靠 `has_more`，实测单课最多 2 页
- 缓存 TTL 6 小时（ID 一旦生成基本不变）

**章节树里为什么没有**：

- `exam.exam_id` —— 实测全站 464 个叶子**无一例外全为空**
- `leaf_info` 详情 —— 对考试/习题返回 `404 Objects does not exist.`

所以用章节树的 id 拼 `/v2/web/exam/...` 一定打不开。

**标题匹配的取舍**：章节树与学习日志之间没有可 join 的字段，
只能按标题关联。实测两边的标题**连全角引号都一致**，因此只做空白归一化
（`normalizeTitle`），不删标点——过度归一化会把不同任务误判成同一个。
匹配失败时静默退回 ai-workspace 路由，不报错。

**三条历史弯路（都写下来，避免重走）**

| 版本 | 当时的错误结论 | 真相 |
|---|---|---|
| v0.4.0 | 用 `#/course/{id}/task` hash 路由 | SPA 是 history 模式，且该路由不存在 |
| v0.6.3 | "全部走 ai-workspace + node id" | 考试/习题各有专属路由，ID 也不在章节树里 |
| v0.6.3 | `iframe-exercise` 用 leafinfo_id | 该路由是旧版 UI，参数语义完全对不上 |
| v0.6.6 | "讨论/作业/视频的决定因素是 leaf_type" | `leaf_type` 同课内会漂移，须以服务端标签为准 |

**v0.6.6 最终路由规则（优先服务端标签）**

路由判定不再看业务类型，而是：

1. 有 `/leaf_info` 详情 → 用 `content_info.score_evaluation.name`
2. 没详情 → 退回 `leaf_type`
3. 试卷类且学习日志给出 `courseware_id` → 升级为老版页面

| 服务端标签 | 路由 | 第二参数 |
|---|---|---|
| 讨论 | `forum/{leaf_id}` | `&node_id={chapter_id}`（**必需**） |
| 作业 / 考试 | `quiz/{leaf_id}` 或老版页 | 升级时用 `courseware_id` |
| 视频 / 图文 / 课堂 / 雨课堂课件 | `video/{leaf_id}` | — |
| 无标签且 leaf_type 未知 | `node/{node_id}` | — |

> 早期文档写过「`node_id` 参数不是必需的——SPA 会自己补上」。这条是**错的**：
> 它是 `chapter_id`，缺了以后页面能打开但内容区为空（v0.6.6 实测）。

**真机验证记录（2026-11-08，v0.6.6 全量复核）**

| 类型 | 链接 | 页面内容 |
|---|---|---|
| 考试 | `exam/24984528/2051258` | 100 分 / 50 题 / 截止 2026-12-31 / 继续答题 |
| 考试 | `exam/23175786/1896478` | 考生须知 / 40 分钟 / 25 道单选 |
| 习题 | `quizSummary/25570085/1745475` | 5 分 / 4 题 / 逐题答案 + 班级正确率 |
| 习题 | `lms-graph/25570088/quiz/48057249?is_chapter=1` | 试卷 100 分 / 26 题 |
| 讨论 | `lms-graph/23174295/forum/41498225?is_chapter=1&node_id=5478455` | 开放型作业 / 讨论区 |
| 课件 | `lms-graph/23175786/video/42162105?is_chapter=1` | 完成度 0% / 讨论区 / 考核截止时间 |
| 视频 | `lms-graph/25570088/video/48057171?is_chapter=1` | 播放器 11:03 + 知识引导 |

<details>
<summary>历史排查笔记（v0.6.3，已被 v0.6.5 / v0.6.6 修正）</summary>

**v0.6.3 当时认为**：所有任务都走 `ai-workspace/lms-graph/:cid/{quiz|video|forum}/:node_id`。

**两个 bug 是真的，结论是片面的**：

- bug #23：ID 用错（`leafinfo_id` vs 节点 id）—— 对习题类成立
- bug #24：漏解析 `section_leaf_list[].leaf_list[]` 三层结构 —— 成立，
  修完任务数 363 → 464

但"考试也走 quiz 路由"是错的。详见上表。

子路由全集（来自 `/fe/static/web/1.2.352/js/aiworkspace.*.js`）：

| 子路由 | 参数 | 实测是否可用 |
|---|---|---|
| `video/:leaf_id` | 叶子 id | ✅ 视频/图文/课件/课堂都能打开 |
| `forum/:leaf_id` | 叶子 id + `node_id=chapter_id` | ✅ 讨论 |
| `quiz/:leaf_id` | 叶子 id | ✅ 仅试卷类 |
| `node/:node_id` | —— | ⚠️ 常报「知识点不存在」 |
| `exercise/:leaf_id` | —— | ❌ 报「页面不存在或学习内容被删除」 |
| `pdf/:leaf_id` | —— | 未测到 |

</details>
### 4.4 去重三态（改这里影响"会不会重复提醒/漏提醒"）

`internal/store/sqlite.go` — `Claim()`

```sql
INSERT INTO pushes (...) VALUES (?, 'claimed', ...)
ON CONFLICT(key) DO UPDATE SET status='claimed'
WHERE pushes.status = 'failed'
```

| 状态 | 语义 | 计入已发送 |
|---|---|---|
| `claimed` | 正在发送的占位 | 否 |
| `sent` | 成功 | 是 |
| `failed` | 失败，可重试 | 否 |
| `suppressed` | 被合并/静默，视为已处理 | 是 |

**改这里务必同步改 `PushedKeys()` 的 `WHERE status IN (...)`。**

### 4.5 首次运行 bootstrap

`internal/scheduler/scheduler.go` — `bootstrap()`

```go
const grace = 24 * time.Hour
// fireAt 早于 now-24h 的里程碑直接记 suppressed
// 保留最近 24h 的（刚截止的仍提醒）
```

**调整策略**：想让「刚过期 3 天内」也提醒，把 `grace` 改成 `72 * time.Hour`。

### 4.6 推送通道（新增通道在这里）

`internal/notify/notifier.go` — `Notifier` 接口

```go
type Notifier interface {
    Name() string
    Send(ctx context.Context, msg Message) error
}
```

新增通道步骤（以邮件为例）：
1. 新建 `internal/notify/email.go`，实现 `Name()` + `Send()`
2. 在 `internal/cli/app.go` 的 `NewRegistry` factories 里注册 `"email"`
3. 在 `configs/config.example.yaml` 加 `notify.email` 段
4. 在 `internal/config/config.go` 加 `EmailConfig` 字段

**文案渲染与通道解耦**：`internal/notify/render.go` 产出 Markdown，
所有通道复用同一份文案，不要在每个通道里各写一套。

### 4.7 登录态

`internal/auth/session.go` — `Cookie` + `Store`

- 落盘 `0600`，原子写入（先 `.tmp` 再 rename）
- `Apply()` 把 cookie 写进请求头，同名 cookie 只出现一次、顺序固定
- `Redacted()` 日志脱敏，`Names()` 返回全部 cookie 名（不含值）
- `Extra map[string]string` 保存 sessionid/csrftoken 之外的**全部** cookie
- `UpdatedAt` = 这次登录建立的时间（导入/扫码），回写不会污染
- `RefreshedAt` = 最近一次收到服务端 Set-Cookie 的时间（**值不变也推进**，
  它是"请求确实通了"的证据，不是"cookie 变了"的记录）
- `ObserveCookie(name, value)` 实现 `ykt.CookieObserver`，落盘有 30 秒节流
- `Flush()` 在进程退出前把节流中的回写补上（`cli.App.Close` 会调）

`internal/auth/qrlogin.go` — 微信扫码六步流程，注意：
- `CheckRedirect` 必须返回 `ErrUseLastResponse`（要拿 302 判断成功）
- 全程复用一个 cookie jar（否则 csrftoken 丢）
- 二维码返回 JPEG
- 登录成功后把 **jar 里的全部 cookie** 存进 `Extra`，不只是两个字段

### 4.7.1 会话寿命：实测结论（2026-11-08，会推翻常见猜测）

**问：为什么官方网页端/App 的登录态能撑很久，我们的很快就过期？**

实测方法：用真实 cookie 逐个打接口，记录响应头。

| 端点 | 下发 Set-Cookie |
|---|---|
| `/v2/api/web/courses/list` | 无 |
| `/mooc-api/v1/lms/learn/course/chapter` | 无 |
| `/api/v3/user/basic-info` | 无 |
| `/v2/web/index`（HTML 页面本身） | 无 |
| `/v/course_meta/user_info` | `csrftoken`（Max-Age 31449600 ≈ 1 年）+ `django_language` |

**结论：雨课堂从不通过 Set-Cookie 续期 `sessionid`。**

`sessionid` 是 Django 的 session key，服务端只认它、不回写它。所以
"捕获响应头 Set-Cookie 自动回写"这条路**救不了**会话过期——那是
常见的 Web 框架行为，但长江雨课堂不是这么实现的。

**那为什么官方端的会话看起来更久？** 三个原因，按把握程度排序：

1. **官方端有活跃的换票链路（最可能）**。网页端在页面级交互里会走
   `/api/v3/user/login/wechat-web-callback` 一类回调重新签发 sessionid，
   而只读 API 客户端不会触发这条链路。
2. **官方端持有更多辅助 cookie**。完整登录后浏览器里有
   `university_id`、`django_language` 等，少带它们时个别接口会降级。
   v0.6.0 起我们改为完整保留（`Cookie.Extra`）。
3. **可能是"活跃即续期"而非固定 TTL**。服务端每次校验会话时可以续期，
   但我们无法从外部观测到内部状态，**这一点未经证实**。

**必须承认的一个错误**：早前根据日志推算"sessionid 寿命约 13.5 小时"
是**错的**。日志显示 `00:29` 后机器关机、`11:55` 才开机，中间 11.44 小时
服务根本没运行。那段空档是关机时间，不是会话存活时间。要测量真实寿命，
必须在服务持续运行的前提下观察，否则数据必然被污染。

**所以 v0.6.0 的回写机制到底有什么用？** 用处在别的地方：
- 记下服务端轮换的 `csrftoken`，避免长期用导入时的旧值（旧值失效会导致 403）
- `/v/course_meta/user_info` 是响应最快的端点，每轮先打它，能最早暴露登录失效
- 面板上"续期 <时间>"这一行是**活性证据**：它变了就说明请求真的通着

**没有做的事（以及为什么）**：
- 没有尝试自动重放 `wechat-web-callback`。那需要 `wx_code`，而 `wx_code`
  只能由真人扫码产生；伪造或缓存它会越界到"绕过登录"的范畴。
- 没有做定时保活去"假装活跃"。那是在对抗风控，不符合项目定位。

### 4.8 面板

`internal/webui/server.go` — 路由 + handler
`internal/webui/assets/{index.html,style.css,app.js}` — 前端（`embed` 打包，无构建步骤）

安全约束：
- 默认只绑 `127.0.0.1`
- 绑非回环地址时必须配 `web_ui.token`，否则拒绝启动（`New()` 里校验）
- `/assets/` 只允许白名单文件名，杜绝路径穿越
- 前端渲染任务列表**全程用 `createElement`**，不用 `innerHTML` 拼接外部数据
  （任务标题来自雨课堂接口，是外部输入）

前端结构（改这里前先看）：
- 布局是「左侧窄导航 + 右侧内容」，`--rail-w` 控制宽度，≤860px 自动折成顶部横条
- 三个显示范围（`state.scope`）：
  - `due`（默认）**只看有截止时间且未过期**——这是提醒应用的主视图。
    363 条任务里 197 条是无截止的视频，不排除会淹没有效信息
  - `open` 未完成全部（含视频）
  - `all` 全部（含已完成/已过期）
- 筛选管线在 `applyFilters()`：范围 → 类型 → 课程 → 关键字
- 筛选状态存 `sessionStorage`（键 `ykt-panel-v2`），损坏时静默回默认
- 类型 chip 角标按「除类型外其他条件」计算，切类型可预览结果量
- 课程下拉以 `classroom_id`（`task.id` 第二段）为去重键；同名课程追加 ID 后缀
- 空态分三种成因分别提示：无数据 / 被筛选挡住（列出具体条件）/ 该范围真的为空

**CSS 陷阱（踩过）**：`.alert`、`.modal` 这类设了 `display:flex|grid` 的元素，
其优先级高于浏览器默认的 `[hidden]{display:none}`，导致 `el.hidden = true` 不生效。
解决方案是全局加一条 `[hidden] { display: none !important; }`。
排障命令：`node --check internal/webui/assets/app.js` 查语法。

**"立即刷新"的真实链路**（v0.4.0 修好，之前是死功能）：

```
前端 POST /api/refresh
  → webui.handleRefresh 往 s.refresh 写信号，响应带 requested_at
  → cli.run 里有个 goroutine 消费 srv.RefreshC()
  → 调 scheduler.Trigger()（带缓冲 1 的 wake 通道）
  → scheduler.Run 的 select 收到唤醒，跳过剩余等待立刻跑下一轮
  → 前端拿 requested_at 轮询 /api/status，等 last_fetch 变新（最多 30s）
```

注意 `Trigger()` 只能让调度器**在当前轮结束后**立刻开始下一轮，不能中断进行中的抓取。

### 4.9 响应信封（解析一切接口的前提）

`internal/ykt/dto.go` — `APIEnvelope[T]`

雨课堂有两种响应格式，**差异是历史上误报"解析失败"的根源**：

```json
成功：{"success":true,"data":{...}}
失败：{"code":50000,"msg":"UNAUTHENTICATED","data":""}
```

- 错误码字段名不同：`errcode` vs `code`
- 错误信息字段名不同：`errmsg` vs `msg`
- 失败时 `data` 是空字符串，直接反序列化到结构体会报错

因此 `APIEnvelope` 自定义了 `UnmarshalJSON`，用 `RawMessage` 接住 `data`
再二次解析；失败时保持零值。所有接口方法统一调 `envelopeErr(env)`，
它负责把 `code=50000` 映射成 `ErrAuthExpired`——**不要绕过它直接用 `env.OK()`**，
否则登录失效又会被当成上游异常。

---

## 五、常用维护任务（照着做）

### 场景 1：雨课堂改了接口字段

```bash
# 1. 生成脱敏快照
./ykt-reminder probe --out probe_out

# 2. 看原始结构（需要更底层时）
python tools/probe_raw.py

# 3. 对比 docs/API_NOTES.md，改 internal/ykt/dto.go 的 json tag

# 4. 跑测试确认
go test ./internal/ykt/
```

`e2e_test.go` 用假服务器锁定了字段名，改错会立刻失败。

### 场景 2：多了个新的任务类型

```bash
# 1. 看类型分布与样例标题
python tools/probe_types.py

# 2. 在 internal/ykt/mapper.go 的 classify() 加关键词
#    或 leafTypeMap 加数字映射

# 3. 在 ykt_test.go 的 TestClassifyPrefersTitle 加一个用例

# 4. 确认该类型是否应有截止时间：
#    在 internal/domain/task.go 的 HasDeadline() 里决定
```

### 场景 3：要加邮件 / QQ / Telegram 推送

见 §4.6。核心是**只实现 Notifier 接口**，不动 scheduler。

### 场景 4：提醒太频繁 / 太少

```yaml
# config.yaml
policy:
  milestones_before_minutes: [1440, 360, 60, 15]  # 改窗口
  coalesce: true        # false 则每个窗口都发
  max_per_cycle: 8      # 单轮上限
  urgent_minutes: 15    # 多少分钟内不受静默限制
```

### 场景 5：cookie 失效了

```bash
./ykt-reminder auth status   # 看是否有效
./ykt-reminder auth login    # 重新扫码（终端直接出 ASCII 二维码）
```

**更方便的方式：用面板扫码**

```bash
./ykt-reminder web           # 起面板（只起面板，不跑提醒循环）
# 浏览器打开 http://127.0.0.1:8787
# 顶部红色横幅里有「微信扫码登录」按钮，点一下出图，手机扫
```

程序在失效时会发**一次**飞书告警（`auth_expired_alerted` KV 标记），
恢复后自动清除标记。若没收到告警，检查 `AlertAuthExpired` 是否被调用
（`cli/run.go` 的 `cmdRun` 里）。

**cookie 寿命无法延长**：雨课堂不给 `sessionid` 下发 Set-Cookie，
所以过期只能重新登录（实测见 §4.7.1）。v0.6.0 起面板左栏显示
`登录 <时间>` / `续期 <时间>` / `N 个 cookie`，其中"续期"时间会随每轮抓取
更新——它是活性证据，能区分"接口还通"和"其实早就失效了"。

生产环境建议：面板常开，收到失效告警时点一下扫码。
`auth import` 也支持直接粘贴浏览器 Cookie（含全部字段），比扫码快。

### 场景 6：发布新版本

```bash
# 1. 改 VERSION
echo "0.2.0" > VERSION

# 2. 全量测试
go test ./... && go vet ./...

# 3. 交叉编译
bash deploy/build.sh

# 4. 更新本文件 §三 的 CHANGELOG
```

---

## 六、待办与迭代方向

### 已完成（v0.2.0）

- [x] **desktop 桌面通知通道** — 零凭据，`internal/notify/desktop.go`
- [x] **webhook 通用 HTTP 通道** — 对接 Bark/Server酱/企微/钉钉/TG，`internal/notify/webhook.go`
- [x] **面板 token 常量时间比较** — `webui.authorized()`
- [x] **`--config` 支持前后两种位置** — `cli.Run`

### 已完成（v0.3.0 · 飞书验证）

- [x] **飞书真实投递验证通过** — `notify test` 返回 `code=0`；
      凭据只走环境变量（`YKT_FEISHU_WEBHOOK` / `YKT_FEISHU_SECRET`），仓库零明文
- [x] **webhook 获取步骤** 已写入 §二（含"自定义机器人"入口、签名校验、常见错误码）

### 已完成（v0.4.0）

- [x] **面板视觉重做** — Linear 式克制风格，三级背景层次、单一主色、
      倒计时符号+颜色+字重三重编码、深色/浅色双主题、响应式
- [x] **任务分类筛选** — 类型 chip（带数量角标）+ 课程下拉（同名自动加 classroom_id 区分）
      + 关键字搜索（150ms 防抖）+ sessionStorage 记忆
- [x] **任务链接修复** — 按类型生成真实路由（`iframe-exercise` / `exam` / `forum` / `video-student`），
      缺参数退回首页。根因见 §三 v0.4.0
- [x] **登录失效误报修复** — `code=50000` + `data:""` 两种格式差异，见 §三 v0.4.0

### 已完成（v0.6.0 · 登录态实测与加固）

- [x] **实测确认会话续期机制** — 5 个端点逐个探测，结论是雨课堂不给
      `sessionid` 下发 Set-Cookie。详见 §4.7.1（**推翻了早前"13.5 小时寿命"的误判**）
- [x] **修重复 cookie（bug #20）** — API client 与登录 client 拆开
- [x] **回写链路接通（bug #21）** — `FetchAll` 每轮触达会话端点
- [x] **完整 cookie 保留（bug #22）** — `Cookie.Extra` + 三种导入格式
- [x] **面板显示登录态健康度** — 登录时间 / 续期时间 / cookie 个数

### 优先级高

- [ ] ~~**多用户支持**~~ → **改用多实例，已实测可行（v0.6.4）**

  **先说结论**：单进程多账号的改造成本远超预期，改用"一个账号一个进程"。

  子代理逐个文件核对后的成本评估（细节见本节末）：

  | 改动项 | 位置 | 风险 |
  |---|---|---|
  | `pushes.key` 全局去重键 | `domain/milestone.go:32-38` + `store/sqlite.go:125-132` | **最严重**：账号 A 发过之后，B 对同一任务的 `Claim` 永远返回 false，B 的提醒被静默吞掉，用户毫无感知 |
  | `tasks` 整表替换 | `store/sqlite.go:208` `DELETE FROM tasks` | A 抓取一轮会删掉 B 的全部快照 |
  | `kv` 全局键 | `auth_expired_alerted`（`scheduler.go:322`）、`tasks_fetched_at`（`sqlite.go:289`） | A 告警一次后，B 的登录失效不再告警 |
  | `ykt.Client` 单会话 | `client.go:78` 单个 `atomic.Pointer[session]` | 缓存键（`client.go:82`、`api.go:92`）不含账号，必然串号 |
  | `CookieObserver` 签名 | `client.go:35-37` 只有 `(name, value)` | 无法判断响应属于哪个账号 |
  | bootstrap 判断 | `scheduler.go:211` `len(pushed) == 0` | 全局判断，B 要么永不 bootstrap，要么一次性补发历史提醒 |

  涉及 **8 个包、约 15-17 个文件、40-60 处代码**，加 300-600 行新逻辑。
  还需要一次数据库 schema 迁移，迁移不当会引发"历史提醒轰炸"。

  **改用多实例**：`data_dir` / `web_ui.listen` / `--config` 本来就都是可配置的，
  每个账号跑一个进程即可完全隔离。实测（2026-11-08）：

  ```
  实例一  8787  已登录  464 个任务  默认 data_dir
  实例二  8799  未登录  0 个任务   独立 data_dir
  ```

  两者 cookie、SQLite、日志完全独立，互不可见。
  代价只是多一个进程（每个约 24MB）。

  配置模板见 `configs/config.second-account.example.yaml`（含独立端口、
  独立数据目录、SMTP 示例）。

  **什么时候才值得做真正的多账号**：需要"一个面板统管所有账号"或
  "统一推送到一个群"时。那时必须先把 `pushes.key` 和 `tasks.id`
  加账号维度，并写数据迁移。

- [ ] **测量 sessionid 真实寿命** — 前提是服务持续运行（这次实测失败就是因为
      中间关机 11.44 小时）。做法：观察日志里第一次 `ErrAuthExpired` 距
      `cookie.json` 的 `updated_at` 有多久，中途不能停服。有了真实数字
      才能判断"每天扫码一次"这类预期是否合理
- [ ] **DevTools Network 挂一天，观察 sessionid 是否自发续期** —
      用户在浏览器登录后开 DevTools → Network，filter `login`，
      保持一天，看有没有非用户触发的 `/api/v3/user/login/*` 请求。
      这一步才能回答"官方端为什么撑得久"（目前只有推断，没有证据）。
      若观察到按固定间隔续期，可考虑让本程序定期访问对应页面级端点。

### 优先级中

#### ✅ 已关闭：详情接口不可用（v0.6.6）

这条曾被误判为"能跑通"：早期认为裸请求返回 `CLASSROOM ID IS REQUIRED`
只是服务端宽容，实际是**所有详情请求 100% 失败**，只是错误被
`APIEnvelope` 吞掉了（不认 `error_code`）。v0.6.6 已修，见 §三 v0.6.6。

#### ✅ 已关闭：任务完成状态数据源（v0.6.6）

曾经以为详情接口有 `is_finish`。实测响应里**没有这个字段**。
真正可用的来源是学习日志的 `status`（3=已完成，0/1=未完成），
v0.6.6 已接入，实测 464 条里 90 条拿到了状态。

#### ✅ 已确认：leaf_type 7（预习/复习材料）

服务端标签是「雨课堂课件」，正确路由是 `video`（与视频共用阅读器）。
走 `quiz` 会报「啊哦，您没有权限查看该页面」，走 `node` 报「知识点不存在」。

#### 其它

- [ ] **课程别名**：当前实测只有 15 个不同课名，`course_name` 已生效，
      不再有"20 多门同名课"的问题。剩余：同名的「面向对象程序设计」分属
      `23175789`/`23175790` 两个课堂，内容 21 条重复——是否合并展示需产品决策
- [ ] **跨班级重复任务**：同名课程不同班级的相同作业会各出一条（如
      「作业1单元」在两个课堂各一份）。去重键目前是 `classroom:leaf`，
      若要合并需按「课程名+标题+截止时间」跨课堂去重
- [ ] **`leaf_type=7` 的业务类型**：服务端标签是「雨课堂课件」，
      但标题可能是「期中2026」「作业1单元」。当前统一显示为"课件"，
      而这些实际可能有截止要求——需确认是否该按标题提升为作业/考试
- [ ] **`期中2026` 拿不到试卷 ID**：它在学习日志里是 `type=2`（非试卷类），
      因此只能退回章节 `quiz` 路由。属数据源限制，非 bug
- [ ] **宽容解析的静默失败**：`FlexTime`/`FlexInt`/`FlexString` 解析失败时
      返回零值而不报错。bug #28 就是被这个策略掩盖的（浮点毫秒→0）。
      可考虑加一条"原始值非空但解析结果为零"的 debug 日志
- [ ] **提醒二次确认**：临近截止时（15 分钟内）可要求点击确认，避免漏看
- [ ] **周报汇总**：每周日推一条「本周待办概览」

### 优先级低

- [ ] 浏览器插件（快速导入 cookie，免手抄）
- [ ] 移动端 PWA 面板
- [ ] 提醒历史导出 CSV
- [ ] 支持荷塘/黄河等其它雨课堂实例（改 `ykt.base_url` 应该可用，需实测）
- [ ] 历史推送记录页的分页（目前只取最近 50 条）
- [ ] `probe_out_tmp/` 目录的临时探针脚本：用完应清理，或整理成
      `ykt probe --dump-tree` 正式子命令（后者更有价值）

### 已知限制（不是 bug）

1. **16/33 门课程 `course_chapter` 为空** —— 雨课堂侧就是空的（历史课/未发布），非解析问题
2. **课件类不提醒** —— 其 `score_deadline` 是课程结束时间，故意排除
3. **同一门课在课程列表出现多次** —— 不同班级共享 `classroom_id`，任务会按 leaf 去重
4. **考试域不访问** —— 提醒不需要，且 cookie 不互通
5. **不提交任何东西** —— 设计上的硬约束，见 `internal/ykt` 无写方法
6. **详情缓存是进程内的** —— 只在常驻进程内生效；`run --once` 每次都是新进程，
   所以单次调试跑出来的 `detail_fetched` 都是全量，看不到缓存收益
7. **面板快照是数据库里的历史数据** —— 改了抓取逻辑后，要跑一次 `run --once`
8. **`leaf_type` 在同课内也会漂移** —— 同一门课里 6/0/4 都可能表示"讨论"。
   因此路由以 `/leaf_info` 的服务端标签为准，`leaf_type` 只是兜底
   或点面板的「立即刷新」才能看到新数据（含新格式的任务链接）
8. **cookie 有效期不受控，且无法通过回写延长** —— 实测雨课堂从不对
   `sessionid` 下发 Set-Cookie（见 §4.7.1），所以会话过期只能重新扫码。
   早期版本在这里写过"实测约 13 小时后失效"，那个数字是**错的**：
   日志显示期间机器关机 11.44 小时，服务没运行，测出来的是关机时长。
   真实寿命需要服务持续运行时才能测。失效后处理见 §五 场景 5

### 会话失效后的正确表现（v0.4.0 起）

以前会显示「上游返回异常: 解析 JSON 失败」，现在应显示：

```
登录状态：已登录（校验失败）
⚠ 登录态已失效，提醒已暂停     [微信扫码登录]
```

若仍看到「解析 JSON 失败」，说明 `APIEnvelope.UnmarshalJSON` 或
`envelopeErr` 被绕过，检查是否有人直接用了 `env.OK()`。

---

## 七、给新 Agent 的接手清单

接手时按顺序做这几件事，10 分钟内能进入状态：

1. **跑测试确认环境**
   ```bash
   go test ./...        # 应全绿（163 个测试函数）
   go vet ./...         # 应无输出
   go test -race ./...  # 见下方说明
   ```

   **本机 `-race` 不可用（环境限制，非代码问题）**：Windows + mingw 环境下
   任何 race 测试都直接 `exit status 0xc0000139`（DLL 入口点缺失），
   连空 `_test.go` 也一样。改用逻辑审查 + 显式并发用例
   （`TestObserveCookieConcurrent`）覆盖，不要因为 race 跑不起来就以为
   代码有竞态。

2. **读三个核心文件**（约 900 行）
   - `internal/domain/policy.go` — 提醒怎么决策
   - `internal/ykt/mapper.go` — 接口数据怎么变成任务
   - `internal/scheduler/scheduler.go` — 一轮怎么跑

3. **跑一次真实流程**
   ```bash
   ./ykt-reminder auth status     # 登录态是否有效
   ./ykt-reminder tasks           # 真实任务列表
   ./ykt-reminder run --once --dry-run  # 不发送地跑一轮
   ```

4. **打开面板看现状**
   ```bash
   ./ykt-reminder web
   # 浏览器 http://127.0.0.1:8787
   ```

5. **确认常驻服务在跑**（Windows 开机自启已注册）
   ```powershell
   Get-ScheduledTask -TaskName YktReminder | Get-ScheduledTaskInfo
   Get-Process ykt-reminder          # 应有 1 个进程，约 24MB
   tail $env:APPDATA\ykt-reminder\ykt.log
   # 关注这行：channels="[desktop feishu]" —— 证明飞书凭据已读到
   ```

6. **改代码前确认依赖方向**（见 §一），不要引入反向 import。

7. **改了接口相关代码后必须**：
   ```bash
   go test ./internal/ykt/    # 端到端字段锁定测试
   ```

### 绝对不要做的事

- ❌ 往 `internal/ykt` 加任何提交/答题/交卷方法（破坏只读约束）
- ❌ 把 webhook / cookie / secret 写进源码或示例文件
- ❌ 绕过 `netguard` 直接请求用户可配 URL（SSRF 风险）
- ❌ 删掉 `Claim` 的 `WHERE status='failed'`（会导致失败永不重试）
- ❌ 删掉「被抑制项也记账」那段（会导致每轮重复提醒）
- ❌ 让 `webui.Listen` 绑 `0.0.0.0` 而不配 token
- ❌ **删掉 `pace()` 或把 `min_request_gap` 调成 0** —— 会恢复成 4 并发瞬时
  请求，是**被风控封号**的最快路径
- ❌ **删掉详情缓存**（`leafInfoCached`）—— 单轮请求量立刻回升到 279 次
- ❌ 用 ASCII 写 VBS 启动器（中文路径会变成问号，静默失败）
- ❌ 把 `.ps1` 存成无 BOM 的 UTF-8（PowerShell 5.1 会按 ANSI 解析而报错）
- ❌ **给业务请求的 `http.Client` 挂 cookie jar** —— `Session.Apply` 已经手工写了
  `Cookie` 头，jar 会把同名 cookie 追加到后面，产生重复（bug #20）。登录流程
  用的是另一个 client（`authHTTP`），两者不能合并
- ❌ **删掉 `FetchAll` 开头的 `CourseMetaUserInfo` 调用** —— 那是全站唯一下发
  Set-Cookie 的端点，删了 `ObserveCookie` 就变成死代码（bug #21）
- ❌ **声称 "Set-Cookie 回写能延长 sessionid 寿命"** —— 实测不成立，见 §4.7.1。
  不要基于错误前提做设计

---

## 八、关键常量速查

| 常量 | 值 | 位置 |
|---|---|---|
| 轮询间隔 | 10m | `config.Default().Poll.Interval` |
| 活跃时段 | 07:00–23:00 | `Poll.ActiveStartHour/EndHour` |
| 非活跃间隔 | 60m | `Poll.IdleInterval` |
| 并发数 | 4 | `Poll.Concurrency` |
| 提醒窗口 | 1440/360/60/15 分钟 | `DefaultMilestoneConfig().BeforeDeadline` |
| 开课提醒 | 提前 10 分钟 | `ClassLeadMinutes` |
| 静默时段 | 23:00–07:00 | `DefaultPolicyConfig()` |
| 静默顺延到 | 07:30 | `QuietDeferHour/Minute` |
| 紧急豁免 | 15 分钟 | `UrgentMinutes` |
| 单轮上限 | 8 条 | `MaxPerCycle` |
| 详情回溯 | 7 天 | `Ykt.DetailLookaheadDays` |
| 章节缓存 | 30 分钟 | `Ykt.ChapterTTL` |
| 详情缓存 | 60 分钟 | `Ykt.DetailTTL` |
| **请求最小间隔** | **300ms** | `Poll.MinRequestGap`（防风控，勿设 0） |
| **远期放宽间隔** | **30 分钟** | `Poll.RelaxedInterval` |
| **放宽阈值** | **6 小时** | `Poll.RelaxHours` |
| Bootstrap 宽限 | 24 小时 | `scheduler.bootstrap()` 的 `grace` |
| 最大前瞻 | 30 天 | `MilestoneConfig.MaxLookahead` |
