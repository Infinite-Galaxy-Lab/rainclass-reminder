package notify

import (
	"net/url"
)

// permissiveGuard 仅用于测试：允许环回与私有地址，
// 以便用 httptest.NewTLSServer（监听 127.0.0.1）验证载荷。
//
// 生产代码绝不使用它。
type permissiveGuardT struct{}

func (permissiveGuardT) ValidateURL(raw string) (*url.URL, error) {
	return url.Parse(raw)
}

func permissiveGuard() guardPolicy { return permissiveGuardT{} }
