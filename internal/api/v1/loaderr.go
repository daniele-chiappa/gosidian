package v1

import (
	"errors"
	"net/http"
	"os"

	"github.com/gosidian/gosidian/internal/vault"
)

// writeLoadError maps a Vault.Load failure to the API error shape: a missing
// note is 404, a path that is not a note file is 400 with the same message
// the write guards use (ADR-021 / IMP-080), anything else is 500.
func writeLoadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, os.ErrNotExist):
		WriteError(w, http.StatusNotFound, CodeNotFound, "note not found")
	case errors.Is(err, vault.ErrNotNote):
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "path must be a note file (.md, or .html when html notes are enabled)")
	default:
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "load: "+err.Error())
	}
}
