package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/supavitW02/tonyTheHelper/api/internal/store"
)

// Length limits are counted in runes, not bytes: Thai is three bytes per character and a
// byte limit would reject perfectly ordinary names.
const (
	maxItemNameLen    = 100
	maxUnitLen        = 30
	maxDescriptionLen = 500
	maxNoteLen        = 300

	// Bounds on a single adjustment. Generous for a household, tight enough that a
	// runaway client cannot write an absurd number into the column.
	maxDelta    = 1000
	maxQuantity = 100000
)

type itemsResponse struct {
	Items []store.Item `json:"items"`
	// Units this household already uses, offered as tappable chips so the same unit
	// does not acquire three spellings.
	Units []string `json:"units"`
}

func (d Deps) listItems(w http.ResponseWriter, r *http.Request, user store.User) {
	householdID := r.PathValue("householdId")

	items, err := d.Items.ItemsForHousehold(r.Context(), householdID, user.ID)
	if err != nil {
		d.itemError(w, err, "list items")
		return
	}
	units, err := d.Items.UnitsForHousehold(r.Context(), householdID, user.ID)
	if err != nil {
		d.itemError(w, err, "list units")
		return
	}

	writeJSON(w, http.StatusOK, itemsResponse{Items: items, Units: units})
}

type createItemRequest struct {
	Name        string `json:"name"`
	Unit        string `json:"unit"`
	Description string `json:"description"`
}

func (d Deps) createItem(w http.ResponseWriter, r *http.Request, user store.User) {
	var body createItemRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	name := strings.TrimSpace(body.Name)
	unit := strings.TrimSpace(body.Unit)
	description := strings.TrimSpace(body.Description)

	switch {
	case name == "":
		writeError(w, http.StatusBadRequest, "item name is required")
		return
	case unit == "":
		writeError(w, http.StatusBadRequest, "unit is required")
		return
	case utf8.RuneCountInString(name) > maxItemNameLen:
		writeError(w, http.StatusBadRequest, "item name is too long")
		return
	case utf8.RuneCountInString(unit) > maxUnitLen:
		writeError(w, http.StatusBadRequest, "unit is too long")
		return
	case utf8.RuneCountInString(description) > maxDescriptionLen:
		writeError(w, http.StatusBadRequest, "description is too long")
		return
	}

	item, err := d.Items.CreateItem(r.Context(), r.PathValue("householdId"), user.ID, name, unit, description)
	if err != nil {
		d.itemError(w, err, "create item")
		return
	}

	writeJSON(w, http.StatusCreated, item)
}

type adjustQuantityRequest struct {
	// Exactly one of these is set: Delta for the plus/minus buttons, Quantity for
	// setting a total after shopping. Pointers so that zero is distinguishable from absent.
	Delta    *int   `json:"delta"`
	Quantity *int   `json:"quantity"`
	Note     string `json:"note"`
}

// adjustQuantity is the only route that changes an item's quantity, and it does no
// arithmetic of its own — the store owns that, so the column and the movement log cannot
// come apart (ADR-0001).
func (d Deps) adjustQuantity(w http.ResponseWriter, r *http.Request, user store.User) {
	var body adjustQuantityRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	switch {
	case (body.Delta == nil) == (body.Quantity == nil):
		writeError(w, http.StatusBadRequest, "send exactly one of delta or quantity")
		return
	case body.Delta != nil && (*body.Delta < -maxDelta || *body.Delta > maxDelta):
		writeError(w, http.StatusBadRequest, "delta is out of range")
		return
	case body.Quantity != nil && (*body.Quantity < 0 || *body.Quantity > maxQuantity):
		writeError(w, http.StatusBadRequest, "quantity is out of range")
		return
	case utf8.RuneCountInString(body.Note) > maxNoteLen:
		writeError(w, http.StatusBadRequest, "note is too long")
		return
	}

	item, err := d.Items.AdjustQuantity(r.Context(), r.PathValue("itemId"), user.ID, store.QuantityChange{
		Delta:    body.Delta,
		Absolute: body.Quantity,
		Note:     strings.TrimSpace(body.Note),
	})
	if err != nil {
		d.itemError(w, err, "adjust quantity")
		return
	}

	writeJSON(w, http.StatusOK, item)
}

// itemError maps a store failure to a response. ErrNotFound covers both "no such thing"
// and "not yours": which households and items exist is not something a non-member gets
// to learn by probing.
func (d Deps) itemError(w http.ResponseWriter, err error, operation string) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	log.Printf("items: %s: %v", operation, err)
	writeError(w, http.StatusInternalServerError, "something went wrong")
}
