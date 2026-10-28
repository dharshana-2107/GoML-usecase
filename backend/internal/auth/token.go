// package auth signs and checks tokens and stores user accounts.
//
// two kinds of signed tokens use the same format:
//   - session tokens live in an httponly cookie after login and say who you are
//   - sale tokens are handed out on the sale page and are needed to check out
//
// both are checked with a quick hmac check, with no database lookup, so every
// api pod can verify them on its own. each kind uses its own key, so a sale
// token can never be used as a session cookie (or the other way around).
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	ErrBadToken     = errors.New("invalid token")
	ErrExpiredToken = errors.New("token expired")
	b64             = base64.RawURLEncoding
	userIDPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{3,64}$`)
)

// ValidUserID keeps junk (and huge strings) out of redis keys and logs.
func ValidUserID(id string) bool { return userIDPattern.MatchString(id) }

// Signer creates and verifies hmac-signed tokens.
type Signer struct {
	secret []byte
	ttl    time.Duration
}

// NewSigner is used for sale tokens.
func NewSigner(secret []byte, ttl time.Duration) Signer {
	return Signer{secret: secret, ttl: ttl}
}

// NewSessionSigner is used for login sessions. it derives a separate key from
// the same secret, so session and sale tokens can't be swapped.
func NewSessionSigner(secret []byte, ttl time.Duration) Signer {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("session-key-v1")) // hash writes never fail
	return Signer{secret: mac.Sum(nil), ttl: ttl}
}

// TTL is how long issued tokens stay valid.
func (s Signer) TTL() time.Duration { return s.ttl }

// Issue returns "base64(userID|expiry).base64(signature)".
func (s Signer) Issue(userID string, now time.Time) (string, time.Time) {
	exp := now.Add(s.ttl)
	payload := userID + "|" + strconv.FormatInt(exp.Unix(), 10)
	return b64.EncodeToString([]byte(payload)) + "." + s.sign(payload), exp
}

// Parse checks the signature and expiry and returns the user id inside.
func (s Signer) Parse(token string, now time.Time) (string, error) {
	encoded, sig, ok := strings.Cut(token, ".")
	if !ok {
		return "", ErrBadToken
	}
	raw, err := b64.DecodeString(encoded)
	if err != nil {
		return "", ErrBadToken
	}
	payload := string(raw)
	// constant time compare so attackers can't guess the signature byte by byte
	if !hmac.Equal([]byte(sig), []byte(s.sign(payload))) {
		return "", ErrBadToken
	}
	userID, expStr, ok := strings.Cut(payload, "|")
	if !ok || !ValidUserID(userID) {
		return "", ErrBadToken
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return "", ErrBadToken
	}
	if now.Unix() > exp {
		return "", ErrExpiredToken
	}
	return userID, nil
}

// Verify checks the token and that it belongs to userID.
func (s Signer) Verify(token, userID string, now time.Time) error {
	owner, err := s.Parse(token, now)
	if err != nil {
		return err
	}
	if owner != userID {
		return ErrBadToken
	}
	return nil
}

func (s Signer) sign(payload string) string {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(payload)) // hash writes never fail
	return b64.EncodeToString(mac.Sum(nil))
}
