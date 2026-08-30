package session_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/supavitW02/tonyTheHelper/api/internal/session"
)

var secret = []byte("a-test-secret-that-is-long-enough-32")

func TestRoundTrip(t *testing.T) {
	m := session.NewManager(secret)

	token, err := m.Issue("user-123")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	got, err := m.UserID(token)
	if err != nil {
		t.Fatalf("UserID() error = %v", err)
	}
	if got != "user-123" {
		t.Errorf("UserID() = %q, want user-123", got)
	}
}

func TestATokenSignedWithAnotherSecretIsRejected(t *testing.T) {
	token, err := session.NewManager([]byte("a-different-secret-also-long-enough!")).Issue("user-123")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	if _, err := session.NewManager(secret).UserID(token); !errors.Is(err, session.ErrNoSession) {
		t.Errorf("error = %v, want ErrNoSession", err)
	}
}

func TestATamperedTokenIsRejected(t *testing.T) {
	m := session.NewManager(secret)
	token, err := m.Issue("user-123")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	// Flip the last character of the signature.
	tampered := token[:len(token)-1] + map[bool]string{true: "A", false: "B"}[strings.HasSuffix(token, "B")]

	if _, err := m.UserID(tampered); !errors.Is(err, session.ErrNoSession) {
		t.Errorf("error = %v, want ErrNoSession", err)
	}
}

// The classic JWT attack: a token that asks to be trusted without a signature.
func TestAnUnsignedTokenIsRejected(t *testing.T) {
	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{
		Subject: "user-123",
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("build unsigned token: %v", err)
	}

	if _, err := session.NewManager(secret).UserID(unsigned); !errors.Is(err, session.ErrNoSession) {
		t.Errorf("error = %v, want ErrNoSession", err)
	}
}

func TestGarbageIsRejected(t *testing.T) {
	if _, err := session.NewManager(secret).UserID("not-a-token"); !errors.Is(err, session.ErrNoSession) {
		t.Errorf("error = %v, want ErrNoSession", err)
	}
}

func TestARequestWithNoCookieHasNoSession(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)

	if _, err := session.NewManager(secret).FromRequest(req); !errors.Is(err, session.ErrNoSession) {
		t.Errorf("error = %v, want ErrNoSession", err)
	}
}

func TestTheCookieRoundTripsThroughARequest(t *testing.T) {
	m := session.NewManager(secret)
	token, err := m.Issue("user-123")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	rec := httptest.NewRecorder()
	m.SetCookie(rec, token)

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	c := cookies[0]
	if !c.HttpOnly {
		t.Error("cookie is not HttpOnly; script could read the session")
	}
	if !c.Secure {
		t.Error("cookie is not Secure")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(c)
	got, err := m.FromRequest(req)
	if err != nil {
		t.Fatalf("FromRequest() error = %v", err)
	}
	if got != "user-123" {
		t.Errorf("FromRequest() = %q, want user-123", got)
	}
}
