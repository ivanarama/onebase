package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID        string
	Login     string
	FullName  string
	IsAdmin   bool
	CreatedAt time.Time
}

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) EnsureSchema(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS _users (
			id UUID PRIMARY KEY,
			login TEXT UNIQUE NOT NULL,
			password_hash BYTEA NOT NULL,
			full_name TEXT NOT NULL DEFAULT '',
			is_admin BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return fmt.Errorf("auth: create _users: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS _sessions (
			token TEXT PRIMARY KEY,
			user_id UUID NOT NULL REFERENCES _users(id) ON DELETE CASCADE,
			expires_at TIMESTAMPTZ NOT NULL
		)`)
	if err != nil {
		return fmt.Errorf("auth: create _sessions: %w", err)
	}
	return nil
}

func (r *Repo) HasUsers(ctx context.Context) (bool, error) {
	var count int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM _users`).Scan(&count)
	return count > 0, err
}

func (r *Repo) List(ctx context.Context) ([]*User, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, login, full_name, is_admin, created_at FROM _users ORDER BY login`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []*User
	for rows.Next() {
		u := &User{}
		if err := rows.Scan(&u.ID, &u.Login, &u.FullName, &u.IsAdmin, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, nil
}

func (r *Repo) Create(ctx context.Context, login, password, fullName string, isAdmin bool) (*User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	id := uuid.New().String()
	_, err = r.pool.Exec(ctx,
		`INSERT INTO _users (id, login, password_hash, full_name, is_admin) VALUES ($1, $2, $3, $4, $5)`,
		id, login, hash, fullName, isAdmin)
	if err != nil {
		return nil, fmt.Errorf("auth: create user: %w", err)
	}
	return &User{ID: id, Login: login, FullName: fullName, IsAdmin: isAdmin}, nil
}

func (r *Repo) Delete(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM _users WHERE id = $1`, id)
	return err
}

func (r *Repo) Authenticate(ctx context.Context, login, password string) (*User, error) {
	u := &User{}
	var hash []byte
	err := r.pool.QueryRow(ctx,
		`SELECT id, login, password_hash, full_name, is_admin FROM _users WHERE login = $1`,
		login).Scan(&u.ID, &u.Login, &hash, &u.FullName, &u.IsAdmin)
	if err != nil {
		return nil, fmt.Errorf("auth: user not found")
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(password)); err != nil {
		return nil, fmt.Errorf("auth: wrong password")
	}
	return u, nil
}

func (r *Repo) CreateSession(ctx context.Context, userID string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	expires := time.Now().Add(24 * time.Hour)
	_, err := r.pool.Exec(ctx,
		`INSERT INTO _sessions (token, user_id, expires_at) VALUES ($1, $2, $3)`,
		token, userID, expires)
	return token, err
}

func (r *Repo) LookupSession(ctx context.Context, token string) (*User, error) {
	u := &User{}
	err := r.pool.QueryRow(ctx, `
		SELECT u.id, u.login, u.full_name, u.is_admin
		FROM _sessions s JOIN _users u ON u.id = s.user_id
		WHERE s.token = $1 AND s.expires_at > now()
	`, token).Scan(&u.ID, &u.Login, &u.FullName, &u.IsAdmin)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (r *Repo) DeleteSession(ctx context.Context, token string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM _sessions WHERE token = $1`, token)
	return err
}
