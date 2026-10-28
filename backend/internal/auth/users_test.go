package auth

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// these tests need postgres with the migrations applied.
// they are skipped when DATABASE_URL is not set.
func newTestStore(t *testing.T) *UserStore {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return NewUserStore(db)
}

func uniqueEmail() string { return "test-" + uuid.NewString()[:8] + "@example.com" }

func TestRegisterThenLogin(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	email := uniqueEmail()

	created, err := store.Register(ctx, "  "+strings.ToUpper(email)+" ", "correct-horse")
	if err != nil {
		t.Fatal(err)
	}
	if created.Email != email || !ValidUserID(created.ID) {
		t.Fatalf("unexpected user %+v", created)
	}

	got, err := store.Login(ctx, email, "correct-horse")
	if err != nil || got.ID != created.ID {
		t.Fatalf("login failed: %+v %v", got, err)
	}
}

func TestLoginFailuresLookTheSame(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	email := uniqueEmail()
	if _, err := store.Register(ctx, email, "correct-horse"); err != nil {
		t.Fatal(err)
	}

	// wrong password and unknown email must give the same error
	if _, err := store.Login(ctx, email, "wrong-password"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("wrong password: got %v", err)
	}
	if _, err := store.Login(ctx, uniqueEmail(), "correct-horse"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("unknown email: got %v", err)
	}
}

func TestRegisterRules(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	email := uniqueEmail()

	if _, err := store.Register(ctx, email, "short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("short password: got %v", err)
	}
	if _, err := store.Register(ctx, "nope", "long-enough-pw"); !errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("bad email: got %v", err)
	}
	if _, err := store.Register(ctx, email, "long-enough-pw"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Register(ctx, strings.ToUpper(email), "another-pw-1"); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("duplicate email: got %v", err)
	}
}
