package ykt

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// OptFloat 是可选浮点数，区分未提供与合法零值。
type OptFloat struct {
	V   float64
	Set bool
}

// UnmarshalJSON 宽容解析数字和数字字符串；非法值按未提供处理。
func (o *OptFloat) UnmarshalJSON(b []byte) error {
	o.V, o.Set = 0, false
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var text string
		if err := json.Unmarshal(b, &text); err != nil {
			return nil
		}
		s = strings.TrimSpace(text)
		if s == "" {
			return nil
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	o.V, o.Set = v, true
	return nil
}

func (o OptFloat) Defined() bool { return o.Set }

func (o OptFloat) Float64() float64 { return o.V }

// Ptr 返回独立副本，避免多个 Task 共享可变值。
func (o OptFloat) Ptr() *float64 {
	if !o.Set {
		return nil
	}
	v := o.V
	return &v
}
