// Package auth handles password hashing and opaque bearer-token issuance
// and verification for API clients (the servergo CLI on each machine).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var ErrInvalidCredentials = errors.New("invalid email or password")
var ErrInvalidToken = errors.New("invalid or expired token")
var ErrEmailTaken = errors.New("email already registered")

type User struct {
	ID    string
	Email string
}

func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(b), nil
}

func Register(ctx context.Context, pool *pgxpool.Pool, email, password string) (*User, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}

	var id string
	err = pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id`,
		email, hash,
	).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrEmailTaken
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}
	return &User{ID: id, Email: email}, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// Login verifies credentials and issues a new opaque bearer token, returning
// the plaintext token (shown to the caller once — only its hash is stored).
func Login(ctx context.Context, pool *pgxpool.Pool, email, password, deviceName string) (token string, user *User, err error) {
	var id, hash string
	err = pool.QueryRow(ctx, `SELECT id, password_hash FROM users WHERE email = $1`, email).Scan(&id, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrInvalidCredentials
	}
	if err != nil {
		return "", nil, fmt.Errorf("lookup user: %w", err)
	}

	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", nil, ErrInvalidCredentials
	}

	token, tokenHash, err := generateToken()
	if err != nil {
		return "", nil, err
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO auth_tokens (user_id, token_hash, device_name) VALUES ($1, $2, $3)`,
		id, tokenHash, deviceName,
	); err != nil {
		return "", nil, fmt.Errorf("insert token: %w", err)
	}

	return token, &User{ID: id, Email: email}, nil
}

func Logout(ctx context.Context, pool *pgxpool.Pool, token string) error {
	_, err := pool.Exec(ctx, `DELETE FROM auth_tokens WHERE token_hash = $1`, hashToken(token))
	return err
}

// Authenticate resolves a bearer token to its owning user and bumps last_used_at.
func Authenticate(ctx context.Context, pool *pgxpool.Pool, token string) (*User, error) {
	if token == "" {
		return nil, ErrInvalidToken
	}
	th := hashToken(token)

	var userID, email string
	err := pool.QueryRow(ctx,
		`SELECT u.id, u.email FROM auth_tokens t JOIN users u ON u.id = t.user_id WHERE t.token_hash = $1`,
		th,
	).Scan(&userID, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, fmt.Errorf("lookup token: %w", err)
	}

	_, _ = pool.Exec(ctx, `UPDATE auth_tokens SET last_used_at = $1 WHERE token_hash = $2`, time.Now(), th)

	return &User{ID: userID, Email: email}, nil
}

func generateToken() (token, tokenHash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	token = hex.EncodeToString(raw)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
