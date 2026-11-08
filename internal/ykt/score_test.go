package ykt

import (
	"encoding/json"
	"testing"
)

func TestOptFloatParsing(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    float64
		defined bool
	}{
		{name: "zero", raw: `0`, want: 0, defined: true},
		{name: "decimal", raw: `0.5`, want: 0.5, defined: true},
		{name: "number string", raw: `"2.75"`, want: 2.75, defined: true},
		{name: "null", raw: `null`, defined: false},
		{name: "empty string", raw: `""`, defined: false},
		{name: "invalid string", raw: `"bad"`, defined: false},
		{name: "boolean", raw: `true`, defined: false},
		{name: "object", raw: `{}`, defined: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got OptFloat
			if err := json.Unmarshal([]byte(tc.raw), &got); err != nil {
				t.Fatalf("解析 %s 失败: %v", tc.raw, err)
			}
			if got.Defined() != tc.defined {
				t.Fatalf("Defined() = %v, want %v", got.Defined(), tc.defined)
			}
			if got.Float64() != tc.want {
				t.Errorf("Float64() = %v, want %v", got.Float64(), tc.want)
			}
		})
	}
}

func TestOptFloatMissingIsUndefined(t *testing.T) {
	var got struct {
		Value OptFloat `json:"value"`
	}
	if err := json.Unmarshal([]byte(`{}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.Value.Defined() || got.Value.Ptr() != nil {
		t.Errorf("缺失字段不应被视为 0: %+v", got.Value)
	}
}

func TestOptFloatRejectsNonFiniteValues(t *testing.T) {
	cases := []string{`NaN`, `Inf`, `-Inf`, `1e999`, `"NaN"`, `"Inf"`}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			got := OptFloat{V: 9, Set: true}
			if err := got.UnmarshalJSON([]byte(raw)); err != nil {
				t.Fatalf("宽容解析不应报错: %v", err)
			}
			if got.Defined() || got.Float64() != 0 {
				t.Errorf("非有限值应重置为未定义，得到 %+v", got)
			}
		})
	}
}

func TestOptFloatUnmarshalResetsPreviousValue(t *testing.T) {
	got := OptFloat{V: 8.5, Set: true}
	for _, raw := range []string{`null`, `""`, `"bad"`} {
		if err := got.UnmarshalJSON([]byte(raw)); err != nil {
			t.Fatal(err)
		}
		if got.Defined() || got.Float64() != 0 {
			t.Errorf("解析 %s 后应重置，得到 %+v", raw, got)
		}
		got = OptFloat{V: 8.5, Set: true}
	}
}

func TestOptFloatPtrReturnsIndependentCopy(t *testing.T) {
	got := OptFloat{V: 0.25, Set: true}
	first := got.Ptr()
	second := got.Ptr()
	if first == nil || second == nil || first == second {
		t.Fatal("每次 Ptr 应返回独立的非 nil 指针")
	}
	*first = 99
	if got.Float64() != 0.25 || *second != 0.25 {
		t.Errorf("修改副本不应影响原值或其它副本: got=%v second=%v", got.Float64(), *second)
	}

	var missing OptFloat
	if missing.Ptr() != nil {
		t.Error("未定义值应返回 nil")
	}
}
