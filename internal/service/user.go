package service

import (
	"context"
	"database/sql"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	queries *database.Queries
}

func NewUser(queries *database.Queries) *User {
	return &User{queries: queries}
}

func (s *User) List(ctx context.Context, limit, offset int64) ([]database.ListUsersRow, error) {
	users, err := s.queries.ListUsers(ctx, database.ListUsersParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, errs.FromDB(err, "list users")
	}
	return users, nil
}

func (s *User) Count(ctx context.Context) (int64, error) {
	count, err := s.queries.CountUsers(ctx)
	if err != nil {
		return 0, errs.FromDB(err, "count users")
	}
	return count, nil
}

func (s *User) Get(ctx context.Context, id int64) (database.User, error) {
	user, err := s.queries.GetUser(ctx, id)
	if err != nil {
		return database.User{}, errs.FromDB(err, "get user")
	}
	return user, nil
}

func (s *User) Create(ctx context.Context, username, password string) (database.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return database.User{}, errs.EInternal("create user", err)
	}

	result, err := s.queries.CreateUser(ctx, database.CreateUserParams{
		Username:     username,
		PasswordHash: sql.NullString{String: string(hash), Valid: true},
		ApiKey:       sql.NullString{},
	})
	if err != nil {
		return database.User{}, errs.FromDB(err, "create user")
	}

	id, err := result.LastInsertId()
	if err != nil {
		return database.User{}, errs.FromDB(err, "last insert id")
	}

	user, err := s.queries.GetUser(ctx, id)
	if err != nil {
		return database.User{}, errs.FromDB(err, "get created user")
	}
	return user, nil
}

func (s *User) Update(ctx context.Context, id int64, username, password string) (database.User, error) {
	user, err := s.queries.GetUser(ctx, id)
	if err != nil {
		return database.User{}, errs.FromDB(err, "update user")
	}

	passwordHash := user.PasswordHash
	if password != "" {
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

	user.Username = username
	user.PasswordHash = passwordHash
	return user, nil
}

func (s *User) Delete(ctx context.Context, id int64) error {
	if err := s.queries.DeleteUser(ctx, id); err != nil {
		return errs.FromDB(err, "delete user")
	}
	return nil
}
