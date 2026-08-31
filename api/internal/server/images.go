package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/supavitW02/tonyTheHelper/api/internal/storage"
	"github.com/supavitW02/tonyTheHelper/api/internal/store"
)

// newImagePath generates a fresh path under the item's own prefix.
//
// A unique name per upload rather than a fixed one, so the browser never has to overwrite
// an existing object — that would need an "x-upsert" header, and a custom header turns a
// simple PUT into a CORS preflight. The old object is left behind; at a family's rate of
// photographing fish sauce, that is not worth a cleanup job.
func newImagePath(itemID string) (string, error) {
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("images: generate path: %w", err)
	}
	return fmt.Sprintf("%s%s.jpg", imagePrefix(itemID), hex.EncodeToString(suffix)), nil
}

// imagePrefix is the only place an item's photos may live, and what a confirmed path is
// checked against.
func imagePrefix(itemID string) string {
	return fmt.Sprintf("items/%s/", itemID)
}

// requestImageUpload hands the browser a short-lived URL to PUT the photo straight to
// object storage. The bytes never pass through this API.
//
// Membership is checked first: a signed URL is a capability, and handing one out for an
// item the caller cannot touch would let them overwrite another household's photo.
func (d Deps) requestImageUpload(w http.ResponseWriter, r *http.Request, user store.User) {
	itemID := r.PathValue("itemId")

	if err := d.Items.ItemAccessible(r.Context(), itemID, user.ID); err != nil {
		d.itemError(w, err, "check item access")
		return
	}

	path, err := newImagePath(itemID)
	if err != nil {
		log.Printf("images: %v", err)
		writeError(w, http.StatusInternalServerError, "could not prepare the upload")
		return
	}

	url, err := d.Storage.SignUpload(r.Context(), path)
	if err != nil {
		log.Printf("images: sign upload: %v", err)
		writeError(w, http.StatusBadGateway, "could not prepare the upload")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"uploadUrl": url, "path": path})
}

type confirmImageRequest struct {
	Path string `json:"path"`
}

// confirmImageUpload records that the photo is now in storage. Separate from the upload so
// the database never claims an image that failed to transfer.
func (d Deps) confirmImageUpload(w http.ResponseWriter, r *http.Request, user store.User) {
	itemID := r.PathValue("itemId")

	var body confirmImageRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	// The path comes back from the client, so it has to be checked: without this an item
	// could be pointed at any object in the bucket, including another household's photo.
	if !strings.HasPrefix(body.Path, imagePrefix(itemID)) || strings.Contains(body.Path, "..") {
		writeError(w, http.StatusBadRequest, "path does not belong to this item")
		return
	}

	item, err := d.Items.SetItemImage(r.Context(), itemID, user.ID, body.Path)
	if err != nil {
		d.itemError(w, err, "set item image")
		return
	}

	writeJSON(w, http.StatusOK, item)
}

// serveImage redirects to a freshly signed read URL.
//
// A redirect rather than a URL embedded in the item JSON: signed URLs expire, and one
// baked into a list response would go stale while the page sat open on a phone. This way
// every load mints a current one, and access is re-checked each time.
func (d Deps) serveImage(w http.ResponseWriter, r *http.Request, user store.User) {
	path, err := d.Items.ItemImagePath(r.Context(), r.PathValue("itemId"), user.ID)
	if err != nil {
		d.itemError(w, err, "item image path")
		return
	}

	url, err := d.Storage.SignDownload(r.Context(), path)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		// The row points at an object that is not there — a failed upload, or a file
		// removed behind the app's back.
		writeError(w, http.StatusNotFound, "not found")
		return
	case err != nil:
		log.Printf("images: sign download: %v", err)
		writeError(w, http.StatusBadGateway, "could not load the image")
		return
	}

	// Not cacheable by shared caches: the URL is per-user and short-lived.
	w.Header().Set("Cache-Control", "private, max-age=60")
	http.Redirect(w, r, url, http.StatusFound)
}
