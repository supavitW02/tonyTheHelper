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

// itemRequest is the writable shape of an item, shared by create and update so the two
// can never drift apart in what they accept.
type itemRequest struct {
	Name        string `json:"name"`
	Unit        string `json:"unit"`
	Description string `json:"description"`
	// Absent or null means the item is never low. Pointer so that 0 — "low as soon as
	// any is used" — is distinguishable from "not set".
	LowStockThreshold *int `json:"lowStockThreshold"`
}

// validate returns a user-facing reason the input is unacceptable, or an empty string.
func (in itemRequest) validate() string {
	switch {
	case strings.TrimSpace(in.Name) == "":
		return "item name is required"
	case strings.TrimSpace(in.Unit) == "":
		return "unit is required"
	case utf8.RuneCountInString(strings.TrimSpace(in.Name)) > maxItemNameLen:
		return "item name is too long"
	case utf8.RuneCountInString(strings.TrimSpace(in.Unit)) > maxUnitLen:
		return "unit is too long"
	case utf8.RuneCountInString(strings.TrimSpace(in.Description)) > maxDescriptionLen:
		return "description is too long"
	case in.LowStockThreshold != nil && (*in.LowStockThreshold < 0 || *in.LowStockThreshold > maxQuantity):
		return "low stock threshold is out of range"
	}
	return ""
}

func (in itemRequest) toInput() store.ItemInput {
	return store.ItemInput{
		Name:              strings.TrimSpace(in.Name),
		Unit:              strings.TrimSpace(in.Unit),
		Description:       strings.TrimSpace(in.Description),
		LowStockThreshold: in.LowStockThreshold,
	}
}

func (d Deps) createItem(w http.ResponseWriter, r *http.Request, user store.User) {
	var body itemRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if problem := body.validate(); problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}

	item, err := d.Items.CreateItem(r.Context(), r.PathValue("householdId"), user.ID, body.toInput())
	if err != nil {
		d.itemError(w, err, "create item")
		return
	}

	writeJSON(w, http.StatusCreated, item)
}

// updateItem changes an item's editable fields. Quantity is deliberately not among them:
// it only ever moves through the quantity endpoint, so every change is recorded.
func (d Deps) updateItem(w http.ResponseWriter, r *http.Request, user store.User) {
	var body itemRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if problem := body.validate(); problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}

	item, err := d.Items.UpdateItem(r.Context(), r.PathValue("itemId"), user.ID, body.toInput())
	if err != nil {
		d.itemError(w, err, "update item")
		return
	}

	writeJSON(w, http.StatusOK, item)
}

// deleteItem soft-deletes an item, keeping its history readable.
func (d Deps) deleteItem(w http.ResponseWriter, r *http.Request, user store.User) {
	if err := d.Items.DeleteItem(r.Context(), r.PathValue("itemId"), user.ID); err != nil {
		d.itemError(w, err, "delete item")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listMovements answers the question a shared household actually asks: who used the last
// one, and when.
func (d Deps) listMovements(w http.ResponseWriter, r *http.Request, user store.User) {
	movements, err := d.Items.MovementsForItem(r.Context(), r.PathValue("itemId"), user.ID)
	if err != nil {
		d.itemError(w, err, "list movements")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"movements": movements})
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
