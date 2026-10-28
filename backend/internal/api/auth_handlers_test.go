package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flashsale/internal/auth"
)

// fakeUsers accepts one account so we can test the http layer without postgres.
type fakeUsers struct{}

func (fakeUsers) Register(_ context.Context, email, _ string) (auth.User, error) {
	return auth.User{ID: "u_new", Email: email}, nil
}

func (fakeUsers) Login(_ context.Context, email, password string) (auth.User, error) {
	if email == "a@b.com" && password == "right-password" {
		return auth.User{ID: "u_1", Email: email}, nil
	}
	return auth.User{}, auth.ErrBadCredentials
}

func testServer() http.Handler {
	return NewServer(Deps{
		Sessions:   auth.NewSessionSigner([]byte("test-secret-test-secret-test-secret"), time.Hour),
		SaleTokens: auth.NewSigner([]byte("test-secret-test-secret-test-secret"), time.Minute),
		Users:      fakeUsers{},
	}).Routes(100)
}

func do(h http.Handler, method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestLoginSetsSafeCookie(t *testing.T) {
	h := testServer()
	res := do(h, "POST", "/api/auth/login", `{"email":"a@b.com","password":"right-password"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("login: got %d %s", res.Code, res.Body)
	}
	cookies := res.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookie {
		t.Fatalf("expected one session cookie, got %v", cookies)
	}
	c := cookies[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.MaxAge <= 0 {
		t.Fatalf("cookie is not locked down: %+v", c)
	}

	// the cookie proves who we are
	me := do(h, "GET", "/api/auth/me", "", c)
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"u_1"`) {
		t.Fatalf("me: got %d %s", me.Code, me.Body)
	}
}

func TestWrongPasswordIsRejected(t *testing.T) {
	res := do(testServer(), "POST", "/api/auth/login", `{"email":"a@b.com","password":"guess"}`)
	if res.Code != http.StatusUnauthorized || len(res.Result().Cookies()) != 0 {
		t.Fatalf("expected 401 and no cookie, got %d", res.Code)
	}
}

func TestForgedOrMissingCookieIsRejected(t *testing.T) {
	h := testServer()
	forged := &http.Cookie{Name: sessionCookie, Value: "dTFfYWRtaW58OTk5OTk5OTk5OQ.fake"}
	for _, res := range []*httptest.ResponseRecorder{
		do(h, "GET", "/api/auth/me", ""),
		do(h, "GET", "/api/auth/me", "", forged),
		do(h, "POST", "/api/sale/token", "", forged),
	} {
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", res.Code)
		}
	}
}

func TestLogoutClearsCookie(t *testing.T) {
	res := do(testServer(), "POST", "/api/auth/logout", "")
	c := res.Result().Cookies()
	if res.Code != http.StatusNoContent || len(c) != 1 || c[0].MaxAge >= 0 {
		t.Fatalf("logout should expire the cookie, got %d %v", res.Code, c)
	}
}

func TestSignedInUserGetsSaleToken(t *testing.T) {
	h := testServer()
	login := do(h, "POST", "/api/auth/login", `{"email":"a@b.com","password":"right-password"}`)
	res := do(h, "POST", "/api/sale/token", "", login.Result().Cookies()[0])
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"token"`) {
		t.Fatalf("sale token: got %d %s", res.Code, res.Body)
	}
}
