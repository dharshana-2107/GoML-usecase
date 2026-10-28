package auth

import (
	"testing"
	"time"
)

func TestTokenRoundTrip(t *testing.T) {
	s := NewSigner([]byte("secret"), time.Minute)
	now := time.Now()
	tok, _ := s.Issue("user-1", now)

	if err := s.Verify(tok, "user-1", now); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if err := s.Verify(tok, "user-2", now); err != ErrBadToken {
		t.Fatalf("token must be bound to its user, got %v", err)
	}
	if err := s.Verify(tok, "user-1", now.Add(2*time.Minute)); err != ErrExpiredToken {
		t.Fatalf("expired token accepted, got %v", err)
	}
}

func TestParseReturnsUser(t *testing.T) {
	s := NewSessionSigner([]byte("secret"), time.Hour)
	tok, _ := s.Issue("u_123", time.Now())
	if id, err := s.Parse(tok, time.Now()); err != nil || id != "u_123" {
		t.Fatalf("expected u_123, got %q %v", id, err)
	}
}

func TestTokenTampering(t *testing.T) {
	s := NewSigner([]byte("secret"), time.Minute)
	other := NewSigner([]byte("another-secret"), time.Minute)
	tok, _ := other.Issue("user-1", time.Now())

	for _, bad := range []string{"", "abc", "abc.def", tok} {
		if err := s.Verify(bad, "user-1", time.Now()); err == nil {
			t.Fatalf("tampered token %q accepted", bad)
		}
	}
}

// a sale token must never work as a login cookie, and the other way around,
// even though both are made from the same secret.
func TestSessionAndSaleTokensAreSeparate(t *testing.T) {
	secret := []byte("one-shared-secret")
	sale := NewSigner(secret, time.Hour)
	session := NewSessionSigner(secret, time.Hour)

	saleTok, _ := sale.Issue("user-1", time.Now())
	if _, err := session.Parse(saleTok, time.Now()); err == nil {
		t.Fatal("sale token accepted as a session")
	}
	sessTok, _ := session.Issue("user-1", time.Now())
	if _, err := sale.Parse(sessTok, time.Now()); err == nil {
		t.Fatal("session token accepted as a sale token")
	}
}

func TestValidUserID(t *testing.T) {
	for id, want := range map[string]bool{
		"user_42": true, "ab": false, "a b c": false, "drop;table": false,
	} {
		if ValidUserID(id) != want {
			t.Errorf("ValidUserID(%q) should be %v", id, want)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	for raw, want := range map[string]string{
		"  Alice@Example.COM ": "alice@example.com",
		"bob@shop.in":          "bob@shop.in",
		"not-an-email":         "",
		"Name <a@b.com>":       "",
		"a@localhost":          "",
	} {
		got, err := NormalizeEmail(raw)
		if (want == "") != (err != nil) || got != want {
			t.Errorf("NormalizeEmail(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
}
