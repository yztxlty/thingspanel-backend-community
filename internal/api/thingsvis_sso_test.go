package api

import (
	"github.com/google/uuid"
	"project/internal/model"
	"testing"
)

func TestThingsVisSSOUsesServerIdentity(t *testing.T) {
	status, authority, tenant := "N", "TENANT_USER", "tenant-owned"
	user := &model.User{ID: uuid.NewString(), Email: "owner@example.com", Status: &status, Authority: &authority, TenantID: &tenant}
	payload, err := thingsVisSSOPayload(user, "validated-token")
	if err != nil {
		t.Fatal(err)
	}
	if payload.Role != "EDITOR" || payload.UserInfo.ID != user.ID || payload.UserInfo.TenantID != tenant {
		t.Fatalf("错误身份映射: %#v", payload)
	}
	authority = "SYS_ADMIN"
	payload, err = thingsVisSSOPayload(user, "validated-token")
	if err != nil || payload.Role != "SUPER_ADMIN" || payload.UserInfo.TenantID != "thingspanel-sys-admin" {
		t.Fatalf("超管空间映射错误: %#v %v", payload, err)
	}
}

func TestThingsVisSSORejectsInactiveOrUnscopedUser(t *testing.T) {
	status, authority, tenant := "D", "TENANT_USER", ""
	user := &model.User{ID: uuid.NewString(), Email: "owner@example.com", Status: &status, Authority: &authority, TenantID: &tenant}
	if _, err := thingsVisSSOPayload(user, "token"); err == nil {
		t.Fatal("不能为禁用用户签发看板身份")
	}
	status = "N"
	if _, err := thingsVisSSOPayload(user, "token"); err == nil {
		t.Fatal("不能回退到公共默认租户")
	}
	if _, err := thingsVisSSOPayload(nil, "token"); err == nil {
		t.Fatal("不能接受空用户")
	}
}
