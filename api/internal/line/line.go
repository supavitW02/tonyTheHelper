// Package line verifies LINE ID tokens.
//
// Verification goes through LINE's own verify endpoint rather than checking the JWT
// signature locally. It costs one HTTPS call per sign-in — not per request, because a
// session is issued afterwards — and in exchange we never handle JWKS rotation, which
// is the part of local verification that quietly breaks months later.
package line

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// VerifyEndpoint is LINE's ID token verification API.
const VerifyEndpoint = "https://api.line.me/oauth2/v2.1/verify"

// ErrInvalidToken is returned for any token LINE rejects: malformed, expired, or
// issued for a different channel. Callers must not distinguish these for the user;
// the answer is always "sign in again".
var ErrInvalidToken = errors.New("line: invalid id token")

// Identity is what LINE tells us about the person holding the token.
type Identity struct {
	UserID      string // stable per channel; the only identity this app has (ADR-0002)
	DisplayName string
	PictureURL  string
}

type Verifier struct {
	ChannelID string
	Endpoint  string       // defaults to VerifyEndpoint
	HTTP      *http.Client // defaults to a client with a timeout
}

func NewVerifier(channelID string) *Verifier {
	return &Verifier{
		ChannelID: channelID,
		Endpoint:  VerifyEndpoint,
		HTTP:      &http.Client{Timeout: 10 * time.Second},
	}
}

type verifyResponse struct {
	Sub     string `json:"sub"`
	Aud     string `json:"aud"`
	Name    string `json:"name"`
	Picture string `json:"picture"`

	Error       string `json:"error"`
	Description string `json:"error_description"`
}

// Verify exchanges an ID token for the identity it asserts, or ErrInvalidToken.
func (v *Verifier) Verify(ctx context.Context, idToken string) (Identity, error) {
	if strings.TrimSpace(idToken) == "" {
		return Identity{}, ErrInvalidToken
	}

	form := url.Values{"id_token": {idToken}, "client_id": {v.ChannelID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.endpoint(), strings.NewReader(form.Encode()))
	if err != nil {
		return Identity{}, fmt.Errorf("line: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := v.client().Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("line: verify request: %w", err)
	}
	defer res.Body.Close()

	var payload verifyResponse
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return Identity{}, fmt.Errorf("line: decode response: %w", err)
	}

	// LINE answers 400 for a token it will not accept, and only then sets `error`.
	if res.StatusCode != http.StatusOK {
		if res.StatusCode >= 500 {
			return Identity{}, fmt.Errorf("line: verify endpoint returned %d", res.StatusCode)
		}
		return Identity{}, fmt.Errorf("%w: %s", ErrInvalidToken, payload.Description)
	}

	// LINE already checks the audience, but it is one comparison and it means a
	// misconfigured channel id fails closed rather than accepting another app's users.
	if payload.Aud != v.ChannelID {
		return Identity{}, fmt.Errorf("%w: audience %q is not this channel", ErrInvalidToken, payload.Aud)
	}
	if payload.Sub == "" {
		return Identity{}, fmt.Errorf("%w: response carried no subject", ErrInvalidToken)
	}

	return Identity{
		UserID:      payload.Sub,
		DisplayName: payload.Name,
		PictureURL:  payload.Picture,
	}, nil
}

func (v *Verifier) endpoint() string {
	if v.Endpoint != "" {
		return v.Endpoint
	}
	return VerifyEndpoint
}

func (v *Verifier) client() *http.Client {
	if v.HTTP != nil {
		return v.HTTP
	}
	return http.DefaultClient
}
