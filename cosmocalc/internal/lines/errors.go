package lines

import (
	"errors"
	"math"
)

// ErrorKind classifies catalogue errors so the API layer can map them to
// structured error bodies distinct from the calculation-core kinds.
type ErrorKind string

const (
	ErrMissingWavelengthUnit     ErrorKind = "missing_wavelength_unit"
	ErrUnitMismatch              ErrorKind = "wavelength_unit_mismatch"
	ErrEmptyCatalog              ErrorKind = "empty_catalog"
	ErrMissingCatalogID          ErrorKind = "missing_catalog_id"
	ErrCatalogNotFound           ErrorKind = "catalog_not_found"
	ErrCatalogIDConflict         ErrorKind = "catalog_id_conflict"
	ErrBuiltinCatalogReadOnly    ErrorKind = "builtin_catalog_read_only"
	ErrMissingLineID             ErrorKind = "missing_line_id"
	ErrDuplicateLineID           ErrorKind = "duplicate_line_id"
	ErrLineNotFound              ErrorKind = "line_not_found"
	ErrNonPositiveLineWavelength ErrorKind = "non_positive_line_wavelength"
	ErrCatalogWavelengthConflict ErrorKind = "catalog_wavelength_unit_conflict"
)

// Error is a typed catalogue error.
type Error struct {
	Kind    ErrorKind
	Field   string
	Message string
}

func (e *Error) Error() string { return e.Message }

func isPositiveFinite(v float64) bool {
	return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}

// AsError unwraps err into a *lines.Error if possible.
func AsError(err error) (*Error, bool) {
	var le *Error
	if errors.As(err, &le) {
		return le, true
	}
	return nil, false
}

// CatalogRepository is the persistence port for custom catalogues. The
// built-in table lives in code and is never stored here. Implementations
// must be safe for concurrent use and return independent deep copies.
type CatalogRepository interface {
	// SaveCatalog persists a new custom catalogue. It returns
	// ErrCatalogIDConflict when the id is already taken.
	SaveCatalog(c *Catalog) error
	// GetCatalog returns the custom catalogue with the given id, or
	// ErrCatalogNotFound.
	GetCatalog(id string) (*Catalog, error)
	// ListCatalogs returns every persisted custom catalogue.
	ListCatalogs() ([]*Catalog, error)
	// UpdateLineWavelength corrects one line's rest wavelength.
	UpdateLineWavelength(catalogID, lineID string, wavelength float64) (*Catalog, error)
}
