// Package storage talks to Supabase Storage.
//
// Image bytes never pass through this API. The browser uploads straight to storage using
// a short-lived signed URL, and reads back through another one, so a phone photo on a slow
// Thai mobile connection is not proxied through a 256MB machine in Singapore.
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ErrNotFound is returned when the object does not exist in the bucket.
var ErrNotFound = errors.New("storage: object not found")

// DownloadTTL is how long a read URL stays valid. Short, because a fresh one is minted
// each time the image is fetched; long enough to survive a slow page load.
const DownloadTTL = 10 * time.Minute

type Client struct {
	baseURL string // project URL, e.g. https://<ref>.supabase.co
	bucket  string
	key     string // service_role key; bypasses RLS, so it never leaves the server
	http    *http.Client
}

func New(baseURL, bucket, serviceRoleKey string) *Client {
	return &Client{
		baseURL: baseURL,
		bucket:  bucket,
		key:     serviceRoleKey,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// SignUpload returns an absolute URL the browser can PUT bytes to without credentials.
func (c *Client) SignUpload(ctx context.Context, path string) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	if err := c.post(ctx, fmt.Sprintf("/storage/v1/object/upload/sign/%s/%s", c.bucket, path), nil, &out); err != nil {
		return "", err
	}
	if out.URL == "" {
		return "", errors.New("storage: upload sign returned no url")
	}
	return c.baseURL + "/storage/v1" + out.URL, nil
}

// SignDownload returns an absolute URL that serves the object without credentials until
// it expires.
func (c *Client) SignDownload(ctx context.Context, path string) (string, error) {
	var out struct {
		SignedURL string `json:"signedURL"`
	}
	body := map[string]any{"expiresIn": int(DownloadTTL.Seconds())}
	if err := c.post(ctx, fmt.Sprintf("/storage/v1/object/sign/%s/%s", c.bucket, path), body, &out); err != nil {
		return "", err
	}
	if out.SignedURL == "" {
		return "", errors.New("storage: download sign returned no url")
	}
	return c.baseURL + "/storage/v1" + out.SignedURL, nil
}

func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	payload := []byte("{}")
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("storage: encode body: %w", err)
		}
		payload = encoded
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("storage: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("storage: request: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("storage: %s returned %d", path, res.StatusCode)
	}

	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("storage: decode response: %w", err)
	}
	return nil
}
