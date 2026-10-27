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

func TestValidUserID(t *testing.T) {
	for id, want := range map[string]bool{
		"user_42": true, "ab": false, "a b c": false, "drop;table": false,
	} {
		if ValidUserID(id) != want {
			t.Errorf("ValidUserID(%q) should be %v", id, want)
		}
	}
}
