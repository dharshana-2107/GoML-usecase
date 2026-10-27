// package auth signs and checks "sale tokens".
// a sale token is handed out when a signed-in user opens the sale page.
// the checkout endpoint refuses any request without a valid one, so a bot
// that just hammers /api/checkout gets rejected before it touches redis.
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
	ErrBadToken     = errors.New("invalid sale token")
	ErrExpiredToken = errors.New("sale token expired")
	b64             = base64.RawURLEncoding
	userIDPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{3,64}$`)
)

// ValidUserID keeps junk (and huge strings) out of redis keys and logs.
func ValidUserID(id string) bool { return userIDPattern.MatchString(id) }

// Signer creates and verifies hmac-signed tokens. no database lookup is
// needed to check a token, so every api pod can verify it on its own.
type Signer struct {
	secret []byte
	ttl    time.Duration
}

func NewSigner(secret []byte, ttl time.Duration) Signer {
	return Signer{secret: secret, ttl: ttl}
}

// Issue returns "base64(userID|expiry).base64(signature)".
func (s Signer) Issue(userID string, now time.Time) (string, time.Time) {
	exp := now.Add(s.ttl)
	payload := userID + "|" + strconv.FormatInt(exp.Unix(), 10)
	return b64.EncodeToString([]byte(payload)) + "." + s.sign(payload), exp
}

// Verify checks the signature, the owner and the expiry time.
func (s Signer) Verify(token, userID string, now time.Time) error {
	encoded, sig, ok := strings.Cut(token, ".")
	if !ok {
		return ErrBadToken
	}
	raw, err := b64.DecodeString(encoded)
	if err != nil {
		return ErrBadToken
	}
	payload := string(raw)
	// constant time compare so attackers can't guess the signature byte by byte
	if !hmac.Equal([]byte(sig), []byte(s.sign(payload))) {
		return ErrBadToken
	}
	owner, expStr, ok := strings.Cut(payload, "|")
	if !ok || owner != userID {
		return ErrBadToken
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return ErrBadToken
	}
	if now.Unix() > exp {
		return ErrExpiredToken
	}
	return nil
}

func (s Signer) sign(payload string) string {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(payload)) // hash writes never fail
	return b64.EncodeToString(mac.Sum(nil))
}
