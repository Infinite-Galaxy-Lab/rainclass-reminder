package ykt

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestEnvelopeSuccess 验证正常成功响应的解析。
func TestEnvelopeSuccess(t *testing.T) {
	var env APIEnvelope[BasicInfoData]
	body := `{"success":true,"data":{"user_id":56106389,"nickname":"张三"}}`
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !env.OK() {
		t.Fatal("应判定为成功")
	}
	if env.Data.UserID.String() != "56106389" {
		t.Fatalf("user_id 解析错误: %q", env.Data.UserID.String())
	}
}

// TestEnvelopeUnauthenticated 验证登录失效响应能被正确识别。
//
// 这是真实踩过的坑：雨课堂未登录时返回
//
//	{"code":50000,"msg":"UNAUTHENTICATED","data":""}
//
// 早期实现只看 errcode 且 data 是强类型对象，导致：
//  1. data:"" 反序列化到结构体报 "cannot unmarshal string into ..."，
//     整个响应被当成"解析失败"；
//  2. 用户看到"上游返回异常"而不是"请重新登录"。
func TestEnvelopeUnauthenticated(t *testing.T) {
	var env APIEnvelope[BasicInfoData]
	body := `{"code":50000,"msg":"UNAUTHENTICATED","data":""}`
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("不应因 data 为空字符串而解析失败: %v", err)
	}
	if env.OK() {
		t.Fatal("code=50000 不应判定为成功")
	}
	if !env.IsAuthFailure() {
		t.Fatal("应识别为登录失效")
	}

	err := envelopeErr(env)
	if !errors.Is(err, ErrAuthExpired) {
		t.Fatalf("应映射为 ErrAuthExpired，实际 %v", err)
	}
}

// TestEnvelopeUpstreamError 验证普通业务错误的分类。
func TestEnvelopeUpstreamError(t *testing.T) {
	var env APIEnvelope[ChapterData]
	body := `{"code":40001,"msg":"invalid param","data":""}`
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if env.IsAuthFailure() {
		t.Fatal("40001 不应判定为登录失效")
	}
	err := envelopeErr(env)
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("应映射为 ErrUpstream，实际 %v", err)
	}
}

// TestEnvelopeErrorCodeField 锁定 bug #27：/mooc-api/* 用 error_code。
//
// 真实响应：
//
//	{"success":false,"error_code":40000,"msg":"CLASSROOM ID IS REQUIRED","data":{}}
//
// 早期 APIEnvelope 只认 errcode / code，两个都不存在 → 都读成 0 →
// OK() 返回 true → 一个明确的错误被当成成功，调用方拿到空结构体继续跑。
// 这是"面板上 464 个任务状态全 unknown"的最后一层原因。
func TestEnvelopeErrorCodeField(t *testing.T) {
	var env APIEnvelope[LeafInfoData]
	body := `{"success":false,"error_code":40000,"msg":"CLASSROOM ID IS REQUIRED","data":{}}`
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if env.OK() {
		t.Fatal("error_code=40000 必须判定为失败")
	}
	if err := envelopeErr(env); !errors.Is(err, ErrUpstream) {
		t.Fatalf("应映射为 ErrUpstream，实际 %v", err)
	}

	// 404000（对象不存在）同样不能被当成成功。
	var env2 APIEnvelope[LeafInfoData]
	if err := json.Unmarshal([]byte(`{"success":false,"error_code":404000,"msg":"Objects does not exist.","data":{}}`), &env2); err != nil {
		t.Fatal(err)
	}
	if env2.OK() {
		t.Fatal("error_code=404000 必须判定为失败")
	}

	// 成功响应：success=true 且无错误码。
	var env3 APIEnvelope[LeafInfoData]
	if err := json.Unmarshal([]byte(`{"success":true,"data":{"id":48057154,"name":"《第一章习题》"}}`), &env3); err != nil {
		t.Fatal(err)
	}
	if !env3.OK() {
		t.Fatal("success=true 应判定为成功")
	}
	if env3.Data.Title.String() != "《第一章习题》" {
		t.Errorf("标题 = %q", env3.Data.Title.String())
	}
}

// TestEnvelopeNoDataField 验证完全缺失 data 字段时不报错。
func TestEnvelopeNoDataField(t *testing.T) {
	var env APIEnvelope[BasicInfoData]
	if err := json.Unmarshal([]byte(`{"success":true}`), &env); err != nil {
		t.Fatalf("缺 data 字段不应报错: %v", err)
	}
	if !env.OK() {
		t.Fatal("无错误码应判定为成功")
	}
}

// TestEnvelopeMessage 验证错误信息兼容两种字段名。
func TestEnvelopeMessage(t *testing.T) {
	var e1 APIEnvelope[BasicInfoData]
	_ = json.Unmarshal([]byte(`{"code":1,"errmsg":"old style"}`), &e1)
	if e1.Message() != "old style" {
		t.Fatalf("errmsg 未读到: %q", e1.Message())
	}

	var e2 APIEnvelope[BasicInfoData]
	_ = json.Unmarshal([]byte(`{"code":1,"msg":"new style"}`), &e2)
	if e2.Message() != "new style" {
		t.Fatalf("msg 未读到: %q", e2.Message())
	}
}
