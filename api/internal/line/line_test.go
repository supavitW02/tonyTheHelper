package line_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supavitW02/tonyTheHelper/api/internal/line"
)

const channelID = "1234567890"

// fakeLINE stands in for LINE's verify endpoint so these tests never touch the network.
func fakeLINE(t *testing.T, status int, body string) *line.Verifier {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if got := r.PostForm.Get("client_id"); got != channelID {
			t.Errorf("client_id = %q, want %q", got, channelID)
		}
		if r.PostForm.Get("id_token") == "" {
			t.Error("id_token was not sent")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return &line.Verifier{ChannelID: channelID, Endpoint: srv.URL, HTTP: srv.Client()}
}

func TestVerifyReturnsTheIdentity(t *testing.T) {
	v := fakeLINE(t, http.StatusOK, `{
		"sub": "U1111111111",
		"aud": "1234567890",
		"name": "สุภวิทย์",
		"picture": "https://profile.line-scdn.net/abc"
	}`)

	got, err := v.Verify(context.Background(), "a.b.c")
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if got.UserID != "U1111111111" {
		t.Errorf("UserID = %q, want U1111111111", got.UserID)
	}
	if got.DisplayName != "สุภวิทย์" {
		t.Errorf("DisplayName = %q, want the Thai display name", got.DisplayName)
	}
	if got.PictureURL != "https://profile.line-scdn.net/abc" {
		t.Errorf("PictureURL = %q", got.PictureURL)
	}
}

func TestVerifyRejectsATokenLINEDoesNotAccept(t *testing.T) {
	v := fakeLINE(t, http.StatusBadRequest, `{"error":"invalid_request","error_description":"IdToken expired."}`)

	_, err := v.Verify(context.Background(), "expired.token.here")
	if !errors.Is(err, line.ErrInvalidToken) {
		t.Errorf("error = %v, want ErrInvalidToken", err)
	}
}

// A token minted for a different LINE channel must not be accepted, even if LINE
// itself answered 200 — otherwise a misconfigured channel id lets in another app's users.
func TestVerifyRejectsATokenForAnotherChannel(t *testing.T) {
	v := fakeLINE(t, http.StatusOK, `{"sub":"U1111111111","aud":"9999999999","name":"Someone"}`)

	_, err := v.Verify(context.Background(), "a.b.c")
	if !errors.Is(err, line.ErrInvalidToken) {
		t.Errorf("error = %v, want ErrInvalidToken", err)
	}
}

func TestVerifyRejectsAnEmptyToken(t *testing.T) {
	v := &line.Verifier{ChannelID: channelID, Endpoint: "http://unused.invalid"}

	if _, err := v.Verify(context.Background(), "   "); !errors.Is(err, line.ErrInvalidToken) {
		t.Errorf("error = %v, want ErrInvalidToken", err)
	}
}

// LINE being down is not the user's fault and must not read as a bad token, or the
// app would tell someone to sign in again during an outage that signing in cannot fix.
func TestVerifyDistinguishesAnOutageFromABadToken(t *testing.T) {
	v := fakeLINE(t, http.StatusInternalServerError, `{}`)

	_, err := v.Verify(context.Background(), "a.b.c")
	if err == nil {
		t.Fatal("Verify() error = nil, want an error")
	}
	if errors.Is(err, line.ErrInvalidToken) {
		t.Errorf("error = %v, want a transport-level error, not ErrInvalidToken", err)
	}
}
