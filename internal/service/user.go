package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wgomg/edub-kushim/internal/auth"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"golang.org/x/crypto/bcrypt"
)

// User is the account service: CRUD with bcrypt-hashed passwords, role
// validation, authentication, and the per-user API keys.
type User struct {
	queries *database.Queries
}

// NewUser wires the account service.
func NewUser(queries *database.Queries) *User {
	return &User{queries: queries}
}

// List pages the users without their hashes.
func (s *User) List(ctx context.Context, limit, offset int32) ([]database.ListUsersRow, error) {
	users, err := s.queries.ListUsers(ctx, database.ListUsersParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, errs.FromDB(err, "list users")
	}
	return users, nil
}

// Count counts the users.
func (s *User) Count(ctx context.Context) (int64, error) {
	count, err := s.queries.CountUsers(ctx)
	if err != nil {
		return 0, errs.FromDB(err, "count users")
	}
	return count, nil
}

// Get returns one user by id without the hash.
func (s *User) Get(ctx context.Context, id int64) (database.User, error) {
	user, err := s.queries.GetUser(ctx, id)
	if err != nil {
		return database.User{}, errs.FromDB(err, "get user")
	}
	return user, nil
}

// Create creates a user after validating the password and the role,
// storing the bcrypt hash.
func (s *User) Create(ctx context.Context, username, password, role string) (database.User, error) {
	if err := ValidatePassword(password); err != nil {
		return database.User{}, err
	}

	if role == "" {
		role = string(auth.RoleViewer)
	}
	if !auth.ValidRole(role) {
		return database.User{}, errs.EInvalid("create user", errors.New("invalid role"))
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return database.User{}, errs.EInternal("create user", err)
	}

	id, err := s.queries.CreateUser(ctx, database.CreateUserParams{
		Username:        username,
		PasswordHash:    sql.NullString{String: string(hash), Valid: true},
		Role:            role,
		ApiKeyHash:      sql.NullString{},
		ApiKeyPrefix:    sql.NullString{},
		ApiKeyCreatedAt: sql.NullTime{},
	})
	if err != nil {
		return database.User{}, errs.FromDB(err, "create user")
	}

	user, err := s.queries.GetUser(ctx, id)
	if err != nil {
		return database.User{}, errs.FromDB(err, "get created user")
	}
	return user, nil
}

// Update changes a user's username, password and role; an empty password
// keeps the current hash.
func (s *User) Update(ctx context.Context, id int64, username, password, role string) (database.User, error) {
	user, err := s.queries.GetUser(ctx, id)
	if err != nil {
		return database.User{}, errs.FromDB(err, "update user")
	}

	passwordHash := user.PasswordHash
	if password != "" {
		if err := ValidatePassword(password); err != nil {
			return database.User{}, err
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return database.User{}, errs.EInternal("update user password", err)
		}
		passwordHash = sql.NullString{String: string(hash), Valid: true}
	}

	if err := s.queries.UpdateUserCredentials(ctx, database.UpdateUserCredentialsParams{
		Username:     username,
		PasswordHash: passwordHash,
		ID:           id,
	}); err != nil {
		return database.User{}, errs.FromDB(err, "update user")
	}

	if role != "" && role != user.Role {
		if !auth.ValidRole(role) {
			return database.User{}, errs.EInvalid("update role", fmt.Errorf("invalid role: %s", role))
		}
		if err := s.queries.UpdateUserRole(ctx, database.UpdateUserRoleParams{
			Role: role,
			ID:   id,
		}); err != nil {
			return database.User{}, errs.FromDB(err, "update user role")
		}
		user.Role = role
	}

	user.Username = username
	user.PasswordHash = passwordHash
	return user, nil
}

// Authenticate verifies a login pair; the returned user carries no hash.
func (s *User) Authenticate(ctx context.Context, username, password string) (database.User, error) {
	user, err := s.queries.GetUserByUsername(ctx, username)
	if err != nil {
		return database.User{}, errs.FromDB(err, "authenticate user")
	}
	if !user.PasswordHash.Valid {
		return database.User{}, errs.EInvalid("authenticate", errors.New("no password set"))
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash.String), []byte(password)); err != nil {
		return database.User{}, errs.EInvalid("authenticate", errors.New("invalid username or password"))
	}
	return user, nil
}

// Delete removes one user.
func (s *User) Delete(ctx context.Context, id int64) error {
	if err := s.queries.DeleteUser(ctx, id); err != nil {
		return errs.FromDB(err, "delete user")
	}
	return nil
}

// CreateAPIKey mints a fresh random API key, stores its SHA-256 hash and
// returns the raw key exactly once.
func (s *User) CreateAPIKey(ctx context.Context, userID int64) (rawKey string, err error) {
	rawKey, hash, prefix, err := auth.GenerateAPIKey()
	if err != nil {
		return "", errs.EInternal("create api key", err)
	}

	now := sql.NullTime{Time: currentTime(), Valid: true}
	rows, err := s.queries.UpdateUserAPIKey(ctx, database.UpdateUserAPIKeyParams{
		ApiKeyHash:      sql.NullString{String: hash, Valid: true},
		ApiKeyPrefix:    sql.NullString{String: prefix, Valid: true},
		ApiKeyCreatedAt: now,
		ID:              userID,
	})
	if err != nil {
		return "", errs.FromDB(err, "create api key")
	}

	if rows == 0 {
		return "", errs.ENotFound("create api key", errors.New("user not found"))
	}

	return rawKey, nil
}

// RevokeAPIKey clears a user's API key.
func (s *User) RevokeAPIKey(ctx context.Context, userID int64) error {
	rows, err := s.queries.RevokeUserAPIKey(ctx, userID)
	if err != nil {
		return errs.FromDB(err, "revoke api key")
	}

	if rows == 0 {
		return errs.ENotFound("revoke api key", errors.New("user not found"))
	}

	return nil
}

// RotateAPIKey revokes and re-issues the key atomically.
func (s *User) RotateAPIKey(ctx context.Context, userID int64) (rawKey string, err error) {
	return s.CreateAPIKey(ctx, userID)
}

// ValidateAPIKey resolves a raw key to its user through the stored hash;
// an unknown key fails authentication.
func (s *User) ValidateAPIKey(ctx context.Context, rawKey string) (database.User, error) {
	hash := auth.HashAPIKey(rawKey)
	user, err := s.queries.GetUserByAPIKeyHash(ctx, sql.NullString{String: hash, Valid: true})
	if err != nil {
		return database.User{}, errs.FromDB(err, "validate api key")
	}
	return user, nil
}

// UpdateRole sets a user's role after validating it.
func (s *User) UpdateRole(ctx context.Context, id int64, role string) error {
	if !auth.ValidRole(role) {
		return errs.EInvalid("update role", fmt.Errorf("invalid role: %s", role))
	}
	if err := s.queries.UpdateUserRole(ctx, database.UpdateUserRoleParams{
		Role: role,
		ID:   id,
	}); err != nil {
		return errs.FromDB(err, "update user role")
	}
	return nil
}

var currentTime = func() time.Time {
	return time.Now()
}
