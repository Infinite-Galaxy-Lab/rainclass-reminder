package notify

import (
	"github.com/2963609262/ykt-reminder/internal/netguard"
)

// defaultWebhookGuard 返回通用 webhook 的 SSRF 策略。
//
// 与飞书通道不同，通用通道允许任意公网 https 域名（Bark/Server酱/
// Telegram 等域名无法穷举），但仍严格拒绝环回、私有、链路本地、
// CGNAT 与保留地址，防止把通知发到内网或云元数据服务。
func defaultWebhookGuard() guardPolicy {
	return netguard.Policy{
		// 不设 AllowHosts：允许任意公网域名。
		AllowHosts:   nil,
		AllowPrivate: false,
		AllowHTTP:    false,
	}
}

// 编译期断言：netguard.Policy 满足 guardPolicy。
var _ guardPolicy = netguard.Policy{}
