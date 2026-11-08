# 推送通道配置

内置四个通道，可在 `notify.enabled` 里按序启用；任一通道发送成功即视为送达。

```yaml
notify:
  enabled:
    - desktop
    - feishu
```

凭据一律优先用环境变量注入，配置文件里保持空值。

---

## 飞书群机器人

```yaml
notify:
  feishu:
    webhook: ""      # 环境变量 YKT_FEISHU_WEBHOOK
    secret: ""       # 环境变量 YKT_FEISHU_SECRET，仅开启签名校验时填
    at_user_id: ""   # open_id，或 "all" 表示 @所有人
```

获取方式：

1. 飞书里建一个群（只拉自己即可）
2. 群设置 → **群机器人** → **添加机器人** → **自定义机器人**
3. 复制 Webhook 地址
4. 可选：在安全设置里勾选**签名校验**，把生成的 secret 一起记下

> 不要启用 IP 白名单或关键词限制，否则会拦掉本程序的请求。

卡片包含任务标题、课程、类型、剩余时间和直达链接。

---

## 桌面通知

```yaml
notify:
  desktop:
    app_name: "雨课堂提醒"
```

无需任何凭据，仅本机可见。适合与远程通道组合，电脑前即时提醒、离开电脑走手机。

依赖系统通知中心：Windows 用 Toast，macOS 用 Notification Center，Linux 走 DBus。

---

## 通用 Webhook

对接任何接受 HTTP POST 的推送服务。

```yaml
notify:
  webhook:
    name: "bark"
    url: ""              # 环境变量 YKT_WEBHOOK_URL
    format: "json"       # json 或 text
    # title_field: "title"
    # body_field: "body"
```

两种载荷格式：

```jsonc
// format: json
{"title": "...", "body": "...", "content": "...", "text": "..."}

// format: text
纯文本正文
```

常用服务（注意 URL 本身含凭据，务必用环境变量）：

| 服务 | URL 形式 | format |
|---|---|---|
| Bark | `https://api.day.app/{key}` | json |
| Server酱 | `https://sctapi.ftqq.com/{key}.send` | json |
| PushPlus | `https://www.pushplus.plus/send?token={token}` | json |
| 企业微信群机器人 | `https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key={key}` | json |
| 钉钉群机器人 | `https://oapi.dingtalk.com/robot/send?access_token={token}` | json |
| Discord | `https://discord.com/api/webhooks/{id}/{token}` | json |
| Telegram | `https://api.telegram.org/bot{token}/sendMessage` | text |

字段名不匹配时用 `title_field` / `body_field` 改名。

目标地址每次发送前都经 SSRF 校验：仅 https，拒绝环回、私有网段、链路本地、CGNAT 与云元数据地址。

---

## SMTP 邮件

适合留档——邮件可检索，也是飞书被企业禁用时的替代。

```yaml
notify:
  email:
    host: ""
    port: 465
    username: ""        # 完整邮箱地址
    password: ""        # 环境变量 YKT_EMAIL_PASSWORD，是授权码不是登录密码
    from: ""            # 留空等于 username
    to: []
    subject_prefix: "【雨课堂提醒】"
    insecure_skip_verify: false
```

常见邮箱参数：

| 邮箱 | 服务器 | 备注 |
|---|---|---|
| QQ | `smtp.qq.com:465` | 设置 → 账户 → 开启 SMTP → 获取授权码 |
| 163 | `smtp.163.com:465` | 同上 |
| Gmail | `smtp.gmail.com:465` | 需两步验证 + 应用专用密码 |
| 校园邮箱 | `smtp.<域名>:465` | 多数开放，端口也可能是 587 |

端口语义：465 建连即 TLS；其它端口强制 STARTTLS，服务端不支持则直接失败——授权码不会明文过网。

`insecure_skip_verify` 仅用于自签名证书的内网邮件服务器。

---

## 测试

```bash
./ykt-reminder notify test
```

逐个通道发送测试消息并打印结果。加上 `--dry-run` 只渲染内容不实际发送。
