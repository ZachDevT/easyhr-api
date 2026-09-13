package identity

import (
	"context"
	"testing"
)

func TestPrincipalContextAndPermission(t *testing.T) {
	principal := Principal{UserID: "user-id", Permissions: map[string]struct{}{"employees:read": {}}}
	ctx := WithPrincipal(context.Background(), principal)

	got, ok := PrincipalFromContext(ctx)
	if !ok || got.UserID != "user-id" {
		t.Fatalf("PrincipalFromContext() = %#v, %t", got, ok)
	}
	if !got.HasPermission("employees:read") || got.HasPermission("employees:write") {
		t.Fatal("HasPermission() returned an unexpected result")
	}
}
