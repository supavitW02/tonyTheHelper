// Package session issues and reads the signed cookie that stands in for a LINE
// sign-in, so the frontend does not re-verify an ID token on every request.
package session

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// CookieName is the session cookie. The app is served from the same origin as the
// API (ADR-0003), so this is a first-party cookie and needs no cross-site handling.
const CookieName = "tth_session"

// DefaultTTL is deliberately long. The people using this open the app rarely, and
// being signed out is a dead end for someone who cannot be told to "just log in again".
const DefaultTTL = 30 * 24 * time.Hour

// ErrNoSession means there is no usable session: absent, malformed, tampered with,
// or expired. Callers must treat them identically — the answer is always "sign in".
var ErrNoSession = errors.New("session: no valid session")

type Manager struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time // swappable so expiry is testable without waiting
}

func NewManager(secret []byte) *Manager {
	return &Manager{secret: secret, ttl: DefaultTTL, now: time.Now}
}

// Issue mints a token for a user id. The token carries no profile data: names and
// pictures change on LINE's side, so they are read from the database, not the cookie.
func (m *Manager) Issue(userID string) (string, error) {
	now := m.now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   userID,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
	})

	signed, err := token.SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("session: sign: %w", err)
	}
	return signed, nil
}

// UserID reads a token and returns the user it belongs to.
func (m *Manager) UserID(token string) (string, error) {
	claims := &jwt.RegisteredClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		// Pin the algorithm. Without this a token could arrive claiming "none".
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return m.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithTimeFunc(m.now))

	if err != nil || !parsed.Valid || claims.Subject == "" {
		return "", ErrNoSession
	}
	return claims.Subject, nil
}

// FromRequest reads the session cookie off a request.
func (m *Manager) FromRequest(r *http.Request) (string, error) {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return "", ErrNoSession
	}
	return m.UserID(cookie.Value)
}

// SetCookie writes the session cookie.
//
// Secure is always on: browsers treat http://localhost as a secure context, so this
// still works in local development, and tunnels used for LIFF development are HTTPS.
func (m *Manager) SetCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(m.ttl.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}
