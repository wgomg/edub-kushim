package database

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func userTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE user (
			id INTEGER PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT,
			api_key TEXT UNIQUE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func TestCreateUser(t *testing.T) {
	db := userTestDB(t)
	q := New(db)

	result, err := q.CreateUser(context.Background(), CreateUserParams{
		Username:     "alice",
		PasswordHash: sql.NullString{String: "hash123", Valid: true},
		ApiKey:       "key-alice",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	id, _ := result.LastInsertId()
	if id == 0 {
		t.Fatal("expected non-zero id")
	}
}

func TestCreateUser_DuplicateUsername(t *testing.T) {
	db := userTestDB(t)
	q := New(db)

	q.CreateUser(context.Background(), CreateUserParams{
		Username: "bob",
		ApiKey:   "key-bob",
	})
	_, err := q.CreateUser(context.Background(), CreateUserParams{
		Username: "bob",
		ApiKey:   "key-bob2",
	})
	if err == nil {
		t.Fatal("expected UNIQUE constraint violation, got nil")
	}
}

func TestGetUser(t *testing.T) {
	db := userTestDB(t)
	q := New(db)

	result, _ := q.CreateUser(context.Background(), CreateUserParams{
		Username: "charlie",
		ApiKey:   "key-charlie",
	})
	id, _ := result.LastInsertId()

	u, err := q.GetUser(context.Background(), id)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if u.Username != "charlie" {
		t.Errorf("Username = %q", u.Username)
	}
}

func TestGetUser_NotFound(t *testing.T) {
	db := userTestDB(t)
	q := New(db)

	_, err := q.GetUser(context.Background(), 999)
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestGetUserByUsername(t *testing.T) {
	db := userTestDB(t)
	q := New(db)

	q.CreateUser(context.Background(), CreateUserParams{
		Username: "dave",
		ApiKey:   "key-dave",
	})

	u, err := q.GetUserByUsername(context.Background(), "dave")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if u.ID == 0 {
		t.Error("expected non-zero id")
	}
}

func TestGetUserByUsername_NotFound(t *testing.T) {
	db := userTestDB(t)
	q := New(db)

	_, err := q.GetUserByUsername(context.Background(), "nonexistent")
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestGetUserByAPIKey(t *testing.T) {
	db := userTestDB(t)
	q := New(db)

	q.CreateUser(context.Background(), CreateUserParams{
		Username: "eve",
		ApiKey:   "key-eve",
	})

	u, err := q.GetUserByAPIKey(context.Background(), "key-eve")
	if err != nil {
		t.Fatalf("GetUserByAPIKey: %v", err)
	}
	if u.Username != "eve" {
		t.Errorf("Username = %q", u.Username)
	}
}

func TestGetUserByAPIKey_NotFound(t *testing.T) {
	db := userTestDB(t)
	q := New(db)

	_, err := q.GetUserByAPIKey(context.Background(), "nonexistent-key")
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestListUsers(t *testing.T) {
	db := userTestDB(t)
	q := New(db)

	q.CreateUser(context.Background(), CreateUserParams{Username: "u1", ApiKey: "k1"})
	q.CreateUser(context.Background(), CreateUserParams{Username: "u2", ApiKey: "k2"})

	users, err := q.ListUsers(context.Background(), ListUsersParams{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("got %d users, want 2", len(users))
	}
}

func TestListUsers_Pagination(t *testing.T) {
	db := userTestDB(t)
	q := New(db)

	q.CreateUser(context.Background(), CreateUserParams{Username: "a", ApiKey: "ka"})
	q.CreateUser(context.Background(), CreateUserParams{Username: "b", ApiKey: "kb"})
	q.CreateUser(context.Background(), CreateUserParams{Username: "c", ApiKey: "kc"})

	users, err := q.ListUsers(context.Background(), ListUsersParams{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("got %d users, want 2", len(users))
	}
}

func TestUpdateUser(t *testing.T) {
	db := userTestDB(t)
	q := New(db)

	result, _ := q.CreateUser(context.Background(), CreateUserParams{
		Username: "frank",
		ApiKey:   "old-key",
	})
	id, _ := result.LastInsertId()

	err := q.UpdateUser(context.Background(), UpdateUserParams{
		Username: "frank-renamed",
		ApiKey:   "new-key",
		ID:       id,
	})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	u, _ := q.GetUser(context.Background(), id)
	if u.Username != "frank-renamed" {
		t.Errorf("Username = %q", u.Username)
	}
}

func TestUpdateUserPassword(t *testing.T) {
	db := userTestDB(t)
	q := New(db)

	result, _ := q.CreateUser(context.Background(), CreateUserParams{
		Username: "grace",
		ApiKey:   "key-grace",
	})
	id, _ := result.LastInsertId()

	err := q.UpdateUserPassword(context.Background(), UpdateUserPasswordParams{
		PasswordHash: sql.NullString{String: "new-hash", Valid: true},
		ID:           id,
	})
	if err != nil {
		t.Fatalf("UpdateUserPassword: %v", err)
	}

	u, _ := q.GetUser(context.Background(), id)
	if !u.PasswordHash.Valid || u.PasswordHash.String != "new-hash" {
		t.Errorf("PasswordHash = %v", u.PasswordHash)
	}
}

func TestDeleteUser(t *testing.T) {
	db := userTestDB(t)
	q := New(db)

	result, _ := q.CreateUser(context.Background(), CreateUserParams{
		Username: "heidi",
		ApiKey:   "key-heidi",
	})
	id, _ := result.LastInsertId()

	err := q.DeleteUser(context.Background(), id)
	if err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	_, err = q.GetUser(context.Background(), id)
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}
}
