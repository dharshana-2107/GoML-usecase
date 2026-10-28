package auth

import (
	"context"
	"errors"
	"net/mail"
	"runtime"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidEmail   = errors.New("invalid email")
	ErrWeakPassword   = errors.New("password must be 8 to 72 characters")
	ErrEmailTaken     = errors.New("email already registered")
	ErrBadCredentials = errors.New("wrong email or password")
)

// bcrypt cost 10 takes about 50-100 ms per hash: slow on purpose, so a stolen
// database can't be brute forced quickly.
const hashCost = 10

// dummyHash is compared against when an email doesn't exist, so "no such
// user" takes as long as "wrong password" and can't be told apart by timing.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("not-a-real-password"), hashCost)

type User struct {
	ID    string `json:"userId"`
	Email string `json:"email"`
}

// UserStore keeps accounts in postgres. it is only used by register and login,
// never by checkout, so the database stays out of the sale's hot path.
type UserStore struct {
	db *pgxpool.Pool
	// hashing is cpu heavy. only a few hashes run at once, so a login rush
	// can't starve the checkout requests running on the same pod.
	slots chan struct{}
}

func NewUserStore(db *pgxpool.Pool) *UserStore {
	return &UserStore{db: db, slots: make(chan struct{}, runtime.NumCPU())}
}

// NormalizeEmail trims and lower cases an email and checks it looks valid.
func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 || !strings.Contains(email, ".") {
		return "", ErrInvalidEmail
	}
	return email, nil
}

// Register creates an account and returns it.
func (u *UserStore) Register(ctx context.Context, rawEmail, password string) (User, error) {
	email, err := NormalizeEmail(rawEmail)
	if err != nil {
		return User{}, err
	}
	if len(password) < 8 || len(password) > 72 { // bcrypt only reads 72 bytes
		return User{}, ErrWeakPassword
	}
	var hash []byte
	err = u.withSlot(ctx, func() (e error) {
		hash, e = bcrypt.GenerateFromPassword([]byte(password), hashCost)
		return e
	})
	if err != nil {
		return User{}, err
	}

	user := User{ID: "u_" + uuid.NewString(), Email: email}
	_, err = u.db.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, $2, $3)`,
		user.ID, user.Email, string(hash))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique violation
		return User{}, ErrEmailTaken
	}
	return user, err
}

// Login checks an email and password and returns the user.
func (u *UserStore) Login(ctx context.Context, rawEmail, password string) (User, error) {
	email, err := NormalizeEmail(rawEmail)
	if err != nil {
		return User{}, ErrBadCredentials
	}
	user := User{Email: email}
	var hash string
	err = u.db.QueryRow(ctx, `SELECT id, password_hash FROM users WHERE email = $1`, email).Scan(&user.ID, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		hash = string(dummyHash) // same amount of work as a real check
	} else if err != nil {
		return User{}, err
	}

	var match bool
	err = u.withSlot(ctx, func() error {
		match = bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
		return nil
	})
	if err != nil {
		return User{}, err
	}
	if !match || user.ID == "" {
		return User{}, ErrBadCredentials
	}
	return user, nil
}

// withSlot waits for a free hashing slot (or gives up when ctx ends).
func (u *UserStore) withSlot(ctx context.Context, fn func() error) error {
	select {
	case u.slots <- struct{}{}:
		defer func() { <-u.slots }()
		return fn()
	case <-ctx.Done():
		return ctx.Err()
	}
}
