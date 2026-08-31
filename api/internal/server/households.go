package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/supavitW02/tonyTheHelper/api/internal/store"
)

// maxHouseholdNameLen is generous for a house name and short enough that the name still
// fits a phone header without truncating.
const maxHouseholdNameLen = 60

type householdsResponse struct {
	Households []store.Household `json:"households"`
	// CurrentID is the household to open. Empty means the user belongs to none yet and
	// should be asked to create one.
	CurrentID string `json:"currentId"`
}

// listHouseholds answers what the app needs on every open: which households am I in, and
// which one should I land in.
func (d Deps) listHouseholds(w http.ResponseWriter, r *http.Request, user store.User) {
	households, err := d.Households.HouseholdsForUser(r.Context(), user.ID)
	if err != nil {
		log.Printf("households: list: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load households")
		return
	}

	writeJSON(w, http.StatusOK, householdsResponse{
		Households: households,
		CurrentID:  currentHousehold(households, user.LastHouseholdID),
	})
}

// currentHousehold picks which household to open.
//
// The stored preference is used only if the user is still a member of it: someone removed
// from a household would otherwise land on a screen they can no longer read. Falling back
// to their first household keeps the app usable without a picker.
func currentHousehold(households []store.Household, preferred string) string {
	if len(households) == 0 {
		return ""
	}
	for _, h := range households {
		if h.ID == preferred {
			return preferred
		}
	}
	return households[0].ID
}

type createHouseholdRequest struct {
	Name string `json:"name"`
}

// createHousehold creates a household with the caller as its owner and first member.
func (d Deps) createHousehold(w http.ResponseWriter, r *http.Request, user store.User) {
	var body createHouseholdRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	name, ok := householdName(w, body.Name)
	if !ok {
		return
	}

	household, err := d.Households.CreateHousehold(r.Context(), user.ID, name)
	if err != nil {
		log.Printf("households: create: %v", err)
		writeError(w, http.StatusInternalServerError, "could not create the household")
		return
	}

	writeJSON(w, http.StatusCreated, household)
}

// householdName trims and validates a submitted name, writing the rejection itself and
// reporting whether the caller should carry on. Shared by creating and renaming, so the
// two can never drift into accepting different names.
func householdName(w http.ResponseWriter, submitted string) (string, bool) {
	name := strings.TrimSpace(submitted)
	switch {
	case name == "":
		writeError(w, http.StatusBadRequest, "household name is required")
		return "", false
	// Counted in runes, not bytes: a Thai name is three bytes per character and a byte
	// limit would reject a perfectly short name.
	case utf8.RuneCountInString(name) > maxHouseholdNameLen:
		writeError(w, http.StatusBadRequest, "household name is too long")
		return "", false
	}
	return name, true
}

type renameHouseholdRequest struct {
	Name string `json:"name"`
}

// renameHousehold changes a household's name after the fact.
//
// Owner only, and the store decides that: a stranger gets 404 rather than 403 so the URL
// cannot be used to discover which household ids exist.
func (d Deps) renameHousehold(w http.ResponseWriter, r *http.Request, user store.User) {
	var body renameHouseholdRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	name, ok := householdName(w, body.Name)
	if !ok {
		return
	}

	household, err := d.Households.RenameHousehold(r.Context(), r.PathValue("householdId"), user.ID, name)
	if err != nil {
		d.householdError(w, err, "rename household")
		return
	}

	writeJSON(w, http.StatusOK, household)
}
