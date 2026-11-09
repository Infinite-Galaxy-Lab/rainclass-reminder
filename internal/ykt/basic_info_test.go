package ykt

import (
	"encoding/json"
	"testing"
)

func TestBasicInfoAccountFieldCompatibility(t *testing.T) {
	for _, tt := range []struct {
		name, body, want string
	}{
		{"current_string_id", `{"id":"101","nickname":"合成昵称"}`, "101"},
		{"numeric_id", `{"id":101}`, "101"},
		{"legacy_user_id", `{"user_id":202}`, "202"},
		{"canonical_precedence", `{"user_id":"202","id":"101"}`, "202"},
		{"null_legacy", `{"user_id":null,"id":"101"}`, "101"},
		{"missing_identity", `{}`, ""},
		{"zero_identity", `{"id":0}`, "0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got BasicInfoData
			if err := json.Unmarshal([]byte(tt.body), &got); err != nil {
				t.Fatal(err)
			}
			if got.UserID.String() != tt.want {
				t.Fatalf("account = %q, want %q", got.UserID.String(), tt.want)
			}
		})
	}
	var reused BasicInfoData
	if err := json.Unmarshal([]byte(`{"id":"101","name":"合成姓名"}`), &reused); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{}`), &reused); err != nil {
		t.Fatal(err)
	}
	if reused.UserID.String() != "" || reused.Name.String() != "" {
		t.Fatal("missing fields retained a previous account")
	}
}
