package server

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/supavitW02/tonyTheHelper/api/internal/store"
)

// createInvite mints an invite and returns the LINE link to send.
//
// The owner never sees or types the token: they get a URL, send it in the chat they
// already use, and the recipient taps it. No code is read aloud over the phone.
func (d Deps) createInvite(w http.ResponseWriter, r *http.Request, user store.User) {
	householdID := r.PathValue("householdId")

	invite, err := d.Households.CreateInvite(r.Context(), householdID, user.ID, time.Now())
	if err != nil {
		d.householdError(w, err, "create invite")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		// A liff.line.me link so tapping it opens the app inside LINE rather than a browser.
		"url":       fmt.Sprintf("https://liff.line.me/%s?invite=%s", d.LiffID, invite.Token),
		"expiresAt": invite.ExpiresAt,
	})
}

// acceptInvite joins the caller to the household the invite belongs to.
func (d Deps) acceptInvite(w http.ResponseWriter, r *http.Request, user store.User) {
	household, err := d.Households.AcceptInvite(r.Context(), r.PathValue("token"), user.ID, time.Now())
	if err != nil {
		d.householdError(w, err, "accept invite")
		return
	}
	writeJSON(w, http.StatusOK, household)
}

func (d Deps) listMembers(w http.ResponseWriter, r *http.Request, user store.User) {
	members, err := d.Households.MembersOf(r.Context(), r.PathValue("householdId"), user.ID)
	if err != nil {
		d.householdError(w, err, "list members")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

// removeMember revokes access. The person and their history are untouched.
func (d Deps) removeMember(w http.ResponseWriter, r *http.Request, user store.User) {
	err := d.Households.RemoveMember(r.Context(), r.PathValue("householdId"), user.ID, r.PathValue("userId"))
	if err != nil {
		d.householdError(w, err, "remove member")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// selectHousehold records which household the user is looking at, so the next open lands
// there. Refuses a household they do not belong to.
func (d Deps) selectHousehold(w http.ResponseWriter, r *http.Request, user store.User) {
	if err := d.Households.SetLastHousehold(r.Context(), user.ID, r.PathValue("householdId")); err != nil {
		d.householdError(w, err, "select household")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// householdError maps store failures onto statuses the UI can act on differently.
func (d Deps) householdError(w http.ResponseWriter, err error, operation string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrNotOwner):
		writeError(w, http.StatusForbidden, "only the household owner can do that")
	case errors.Is(err, store.ErrInviteExpired):
		// Gone rather than a generic error: the link was real, it has simply run out.
		writeError(w, http.StatusGone, "this invite has expired")
	case errors.Is(err, store.ErrInviteUsed):
		writeError(w, http.StatusConflict, "this invite has already been used")
	case errors.Is(err, store.ErrCannotRemoveOwner):
		writeError(w, http.StatusBadRequest, "the household owner cannot be removed")
	default:
		log.Printf("households: %s: %v", operation, err)
		writeError(w, http.StatusInternalServerError, "something went wrong")
	}
}
