package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"flashsale/internal/auth"
)

// the cookie that holds the signed session token
const sessionCookie = "fs_session"

// Users is the part of the user store the handlers need (easy to fake in tests).
type Users interface {
	Register(ctx context.Context, email, password string) (auth.User, error)
	Login(ctx context.Context, email, password string) (auth.User, error)
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	c, ok := readCredentials(w, r)
	if !ok {
		return
	}
	user, err := s.Users.Register(r.Context(), c.Email, c.Password)
	switch {
	case errors.Is(err, auth.ErrInvalidEmail):
		writeJSON(w, http.StatusBadRequest, errBody("invalid_email"))
	case errors.Is(err, auth.ErrWeakPassword):
		writeJSON(w, http.StatusBadRequest, errBody("weak_password"))
	case errors.Is(err, auth.ErrEmailTaken):
		writeJSON(w, http.StatusConflict, errBody("email_taken"))
	case err != nil:
		fail(w, http.StatusServiceUnavailable, "try_again", err)
	default:
		s.startSession(w, user.ID)
		writeJSON(w, http.StatusCreated, user)
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	c, ok := readCredentials(w, r)
	if !ok {
		return
	}
	user, err := s.Users.Login(r.Context(), c.Email, c.Password)
	switch {
	case errors.Is(err, auth.ErrBadCredentials):
		// same answer for "no such user" and "wrong password"
		writeJSON(w, http.StatusUnauthorized, errBody("wrong_email_or_password"))
	case err != nil:
		fail(w, http.StatusServiceUnavailable, "try_again", err)
	default:
		s.startSession(w, user.ID)
		writeJSON(w, http.StatusOK, user)
	}
}

// logout deletes the cookie. sessions are stateless, so a copied token stays
// valid until it expires; see docs/DESIGN.md for the production fix.
func (s *Server) logout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, s.cookie("", -1))
	w.WriteHeader(http.StatusNoContent)
}

// me tells the browser who is signed in. it only checks the cookie signature,
// so 100k page loads at 8 pm never touch the database.
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errBody("not_signed_in"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"userId": userID})
}

// currentUser reads the user id from the signed session cookie.
func (s *Server) currentUser(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", false
	}
	userID, err := s.Sessions.Parse(c.Value, time.Now())
	return userID, err == nil
}

func (s *Server) startSession(w http.ResponseWriter, userID string) {
	token, _ := s.Sessions.Issue(userID, time.Now())
	http.SetCookie(w, s.cookie(token, int(s.Sessions.TTL().Seconds())))
}

// cookie builds the session cookie with safe settings:
//   - HttpOnly: javascript can't read it, so an xss bug can't steal it
//   - SameSite=Strict: other sites can't make the browser send it (csrf)
//   - Secure: https only (turned on in production with COOKIE_SECURE=true)
func (s *Server) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name: sessionCookie, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.SecureCookie,
	}
}

// readCredentials parses a small json body like {"email": "...", "password": "..."}.
func readCredentials(w http.ResponseWriter, r *http.Request) (credentials, bool) {
	var c credentials
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10) // nobody needs more than 4 kb to log in
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid_json"))
		return c, false
	}
	return c, true
}
