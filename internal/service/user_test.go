package service

import (
	"context"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

func newTestUserService(t *testing.T) (*User, *database.Client) {
	t.Helper()
	client := database.NewTestClient(t)
	t.Cleanup(func() { client.DB().Close() })
	return NewUser(client.Queries), client
}

func TestUser_CreateAPIKey(t *testing.T) {
	svc, _ := newTestUserService(t)
	ctx := context.Background()

	user, err := svc.Create(ctx, "alice", "Password123!", "viewer")
	testutil.AssertNoError(t, err, "create user")

	rawKey, err := svc.CreateAPIKey(ctx, user.ID)
	testutil.AssertNoError(t, err, "create api key")

	if !strings.HasPrefix(rawKey, "ek_") {
		t.Fatalf("expected raw key to start with ek_, got %q", rawKey)
	}
	if len(rawKey) != 3+64 {
		t.Fatalf("expected raw key length 67, got %d", len(rawKey))
	}

	found, err := svc.ValidateAPIKey(ctx, rawKey)
	testutil.AssertNoError(t, err, "validate created key")
	testutil.AssertEqual(t, found.ID, user.ID, "user ID matches")
}

func TestUser_CreateAPIKey_UserNotFound(t *testing.T) {
	svc, _ := newTestUserService(t)
	ctx := context.Background()

	_, err := svc.CreateAPIKey(ctx, 9999)
	testutil.AssertError(t, err, "expected error for non-existent user")
	if errs.KindOf(err) != errs.KindNotFound {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestUser_RevokeAPIKey(t *testing.T) {
	svc, _ := newTestUserService(t)
	ctx := context.Background()

	user, _ := svc.Create(ctx, "alice", "Password123!", "viewer")
	rawKey, _ := svc.CreateAPIKey(ctx, user.ID)

	err := svc.RevokeAPIKey(ctx, user.ID)
	testutil.AssertNoError(t, err, "revoke api key")

	_, err = svc.ValidateAPIKey(ctx, rawKey)
	testutil.AssertError(t, err, "validate revoked key should fail")
	if errs.KindOf(err) != errs.KindNotFound {
		t.Fatalf("expected not-found after revoke, got %v", err)
	}

	userAfter, err := svc.Get(ctx, user.ID)
	testutil.AssertNoError(t, err, "get user after revoke")
	if userAfter.ApiKeyHash.Valid {
		t.Fatal("expected api_key_hash to be NULL after revocation")
	}
}

func TestUser_RevokeAPIKey_UserNotFound(t *testing.T) {
	svc, _ := newTestUserService(t)
	ctx := context.Background()

	err := svc.RevokeAPIKey(ctx, 9999)
	testutil.AssertError(t, err, "expected error for non-existent user")
	if errs.KindOf(err) != errs.KindNotFound {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestUser_RotateAPIKey(t *testing.T) {
	svc, _ := newTestUserService(t)
	ctx := context.Background()

	user, _ := svc.Create(ctx, "alice", "Password123!", "viewer")
	firstKey, _ := svc.CreateAPIKey(ctx, user.ID)

	secondKey, err := svc.RotateAPIKey(ctx, user.ID)
	testutil.AssertNoError(t, err, "rotate api key")

	if firstKey == secondKey {
		t.Fatal("expected different key after rotation")
	}
	if !strings.HasPrefix(secondKey, "ek_") {
		t.Fatalf("expected rotated key to start with ek_, got %q", secondKey)
	}

	_, err = svc.ValidateAPIKey(ctx, firstKey)
	testutil.AssertError(t, err, "old key should be invalid after rotation")

	found, err := svc.ValidateAPIKey(ctx, secondKey)
	testutil.AssertNoError(t, err, "new key should be valid")
	testutil.AssertEqual(t, found.ID, user.ID, "user ID matches after rotation")
}

func TestUser_ValidateAPIKey(t *testing.T) {
	svc, _ := newTestUserService(t)
	ctx := context.Background()

	user, _ := svc.Create(ctx, "alice", "Password123!", "viewer")
	rawKey, _ := svc.CreateAPIKey(ctx, user.ID)

	found, err := svc.ValidateAPIKey(ctx, rawKey)
	testutil.AssertNoError(t, err, "validate api key")
	testutil.AssertEqual(t, found.ID, user.ID, "user ID matches")
	testutil.AssertEqual(t, found.Username, "alice", "username matches")

	_, err = svc.ValidateAPIKey(ctx, "ek_wrongkey1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab")
	testutil.AssertError(t, err, "expected error for wrong key")
	if errs.KindOf(err) != errs.KindNotFound {
		t.Fatalf("expected not-found for invalid key, got %v", err)
	}
}

func TestUser_Create_RoleDefaultsToViewer(t *testing.T) {
	svc, _ := newTestUserService(t)
	ctx := context.Background()

	user, err := svc.Create(ctx, "bob", "Password123!", "")
	testutil.AssertNoError(t, err, "create user with empty role")
	testutil.AssertEqual(t, user.Role, "viewer", "default role")
}

func TestUser_Create_InvalidRoleRejected(t *testing.T) {
	svc, _ := newTestUserService(t)
	ctx := context.Background()

	_, err := svc.Create(ctx, "bob", "Password123!", "superadmin")
	testutil.AssertError(t, err, "expected error for invalid role")
	if errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("expected invalid error, got %v", err)
	}
}

func TestUser_Create_ExplicitRoles(t *testing.T) {
	svc, _ := newTestUserService(t)
	ctx := context.Background()

	for _, role := range []string{"admin", "editor", "viewer"} {
		user, err := svc.Create(ctx, role+"user", "Password123!", role)
		testutil.AssertNoError(t, err, "create user with role "+role)
		testutil.AssertEqual(t, user.Role, role, "role")
	}
}

func TestUser_UpdateRole(t *testing.T) {
	svc, _ := newTestUserService(t)
	ctx := context.Background()

	user, err := svc.Create(ctx, "alice", "Password123!", "viewer")
	testutil.AssertNoError(t, err, "create user")

	err = svc.UpdateRole(ctx, user.ID, "admin")
	testutil.AssertNoError(t, err, "update role to admin")

	updated, err := svc.Get(ctx, user.ID)
	testutil.AssertNoError(t, err, "get user")
	testutil.AssertEqual(t, updated.Role, "admin", "role after update")
}

func TestUser_UpdateRole_InvalidRole(t *testing.T) {
	svc, _ := newTestUserService(t)
	ctx := context.Background()

	user, err := svc.Create(ctx, "alice", "Password123!", "viewer")
	testutil.AssertNoError(t, err, "create user")

	err = svc.UpdateRole(ctx, user.ID, "superadmin")
	testutil.AssertError(t, err, "expected error for invalid role")
	if errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("expected invalid error, got %v", err)
	}
}

func TestUser_UpdateWithRole(t *testing.T) {
	svc, _ := newTestUserService(t)
	ctx := context.Background()

	user, err := svc.Create(ctx, "alice", "Password123!", "viewer")
	testutil.AssertNoError(t, err, "create user")
	testutil.AssertEqual(t, user.Role, "viewer", "initial role")

	updated, err := svc.Update(ctx, user.ID, "alice2", "", "editor")
	testutil.AssertNoError(t, err, "update with role change")
	testutil.AssertEqual(t, updated.Username, "alice2", "updated username")
	testutil.AssertEqual(t, updated.Role, "editor", "updated role")

	fetched, err := svc.Get(ctx, user.ID)
	testutil.AssertNoError(t, err, "get user after update")
	testutil.AssertEqual(t, fetched.Role, "editor", "role persisted")
}

func TestUser_UpdateWithoutRoleChange(t *testing.T) {
	svc, _ := newTestUserService(t)
	ctx := context.Background()

	user, err := svc.Create(ctx, "alice", "Password123!", "editor")
	testutil.AssertNoError(t, err, "create user")

	updated, err := svc.Update(ctx, user.ID, "alice2", "", "")
	testutil.AssertNoError(t, err, "update without role")
	testutil.AssertEqual(t, updated.Role, "editor", "role unchanged")
}

