package ykt

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// 本文件处理雨课堂 API 的宽容解析。
//
// 该 API 的字段类型不稳定：同一个字段在字符串与数字之间摇摆，时间戳有时
// 是秒、有时是毫秒、有时是格式化字符串。所有可能变化的标量都包一层自定义
// 类型，解析失败退化为零值而非报错，保证一个坏字段不影响整个任务列表。

// FlexInt 兼容 123 / "123" / 123.0 / null。
type FlexInt int64

// UnmarshalJSON 实现宽容整数解析。
func (f *FlexInt) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == `""` || s == "" {
		*f = 0
		return nil
	}
	s = strings.Trim(s, `"`)
	if s == "" {
		*f = 0
		return nil
	}
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		*f = FlexInt(v)
		return nil
	}
	// 可能是 "123.0" 或 123.0
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		*f = FlexInt(int64(v))
		return nil
	}
	*f = 0
	return nil // 宽容：不因单个字段报错
}

// Int64 返回原生类型。
func (f FlexInt) Int64() int64 { return int64(f) }

// String 返回十进制字符串。
func (f FlexInt) String() string { return strconv.FormatInt(int64(f), 10) }

// FlexString 兼容 "abc" / 123 / null / 嵌套对象（退化空串）。
type FlexString string

// UnmarshalJSON 实现宽容字符串解析。
func (f *FlexString) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*f = ""
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			*f = ""
			return nil
		}
		*f = FlexString(v)
		return nil
	}
	// 数字/布尔等直接取字面量
	*f = FlexString(s)
	return nil
}

// String 返回原生字符串。
func (f FlexString) String() string { return string(f) }

// FlexBool 兼容 true / false / 1 / 0 / "true" / "1" / null。
//
// 详情接口的 is_locked / is_deleted / is_assessed 是 JSON 布尔值，
// 而 FlexInt 会把 true 和 false 都解析成 0，两者无法区分。
type FlexBool bool

// UnmarshalJSON 实现宽容布尔解析。
func (f *FlexBool) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(strings.Trim(strings.TrimSpace(string(b)), `"`))
	switch strings.ToLower(s) {
	case "true", "1", "yes", "on":
		*f = true
	case "false", "0", "no", "off", "", "null":
		*f = false
	default:
		*f = false // 宽容：未知取值不报错
	}
	return nil
}

// Bool 返回原生类型。
func (f FlexBool) Bool() bool { return bool(f) }

// FlexTime 兼容多种时间表示，统一解析为 Unix 秒。
//
// 支持：
//   - Unix 秒 / 毫秒（通过量级判断）
//   - "2026-11-10 12:00:00"
//   - "2026-11-10T12:00:00+08:00"（RFC3339）
//   - "2026/11/10 12:00"
//
// 解析失败返回 0（表示"无时间"），而不是整体报错。
type FlexTime int64

// 支持的本地时间格式（雨课堂主要使用前两种）。
var timeLayouts = []string{
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02T15:04:05",
	"2006/01/02 15:04:05",
	"2006-01-02 15:04",
	"2006/01/02 15:04",
	"2006-01-02",
	"15:04:05",
}

// UnmarshalJSON 实现宽容时间解析。
func (f *FlexTime) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == `""` || s == "" {
		*f = 0
		return nil
	}
	s = strings.Trim(s, `"`)

	// 纯数字：按量级区分秒/毫秒。
	//
	// 必须同时支持整数与浮点：/leaf_info 返回的毫秒时间戳带小数
	// （`"score_deadline": 1778428740000.0`），ParseInt 会解析失败，
	// 进而被下面的"失败返回 0"吞掉，表现为截止时间凭空消失。
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		*f = FlexTime(normalizeEpoch(n))
		return nil
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		*f = FlexTime(normalizeEpoch(int64(v)))
		return nil
	}

	for _, layout := range timeLayouts {
		if t, err := parseInLocation(layout, s); err == nil {
			*f = FlexTime(t)
			return nil
		}
	}
	*f = 0
	return nil
}

// Unix 返回秒级时间戳（0 表示无时间）。
func (f FlexTime) Unix() int64 { return int64(f) }

// 拆出以便单测替换（当前直接使用 time.Local）。
func parseInLocation(layout, value string) (int64, error) {
	t, err := time.ParseInLocation(layout, value, time.Local)
	if err != nil {
		return 0, err
	}
	return t.Unix(), nil
}

// normalizeEpoch 把毫秒时间戳归一到秒；小于 1e12 视为秒。
func normalizeEpoch(n int64) int64 {
	if n > 1e12 {
		return n / 1000
	}
	return n
}
