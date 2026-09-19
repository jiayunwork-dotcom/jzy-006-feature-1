// Package lines defines the rest-frame spectral-line catalog domain
// types: the built-in default catalog, caller-supplied catalogs, the
// per-identification snapshot, and catalog validation. It contains no
// I/O and no dependencies beyond the standard library.
package lines

import (
	"fmt"
	"math"
	"time"
)

// Line is one rest-frame spectral line. ID is its stable identity
// (e.g. "Hα", "[OIII]"); RestWavelength is expressed in the catalog's
// declared unit.
type Line struct {
	ID             string  `json:"id"`
	RestWavelength float64 `json:"rest_wavelength"`
}

// Catalog is a named rest-line table. Every line wavelength is
// interpreted strictly in the declared Unit; the service never converts
// between units — a unit mismatch with the observed peaks is rejected.
type Catalog struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Unit  string `json:"unit"`
	Lines []Line `json:"lines"`
}

// DefaultCatalogID is the reserved identity of the built-in catalog.
const DefaultCatalogID = "default"

// InlineCatalogID labels ad-hoc catalogs embedded in a request.
const InlineCatalogID = "inline"

// ErrorKind classifies catalog/line validation errors so the API layer
// can map them to distinct structured error responses.
type ErrorKind string

const (
	ErrMissingUnit           ErrorKind = "missing_unit"
	ErrEmptyCatalog          ErrorKind = "empty_catalog"
	ErrInvalidLineWavelength ErrorKind = "invalid_line_wavelength"
	ErrMissingLineID         ErrorKind = "missing_line_id"
	ErrDuplicateLineID       ErrorKind = "duplicate_line_id"
)

// Error is a typed catalog validation error.
type Error struct {
	Kind    ErrorKind
	Field   string
	Message string
}

func (e *Error) Error() string { return e.Message }

// defaultLines is the built-in optical emission-line catalog (unit: nm).
// It covers the benchmark lines Hα 656.28, Hβ 486.13 and [OIII] 500.7
// plus enough other common optical lines that a single observed peak
// matches several of them at different redshifts (i.e. is ambiguous).
var defaultLines = []Line{
	{ID: "[OII] 372.7", RestWavelength: 372.7},
	{ID: "Hδ", RestWavelength: 410.17},
	{ID: "Hγ", RestWavelength: 434.05},
	{ID: "Hβ", RestWavelength: 486.13},
	{ID: "[OIII] 495.9", RestWavelength: 495.9},
	{ID: "[OIII]", RestWavelength: 500.7},
	{ID: "He I 587.6", RestWavelength: 587.56},
	{ID: "[NII] 654.8", RestWavelength: 654.8},
	{ID: "Hα", RestWavelength: 656.28},
	{ID: "[NII] 658.3", RestWavelength: 658.34},
	{ID: "[SII] 671.6", RestWavelength: 671.64},
	{ID: "[SII] 673.1", RestWavelength: 673.08},
}

// DefaultCatalog returns the built-in optical emission-line catalog.
// Each call returns an independent copy; mutating it affects nothing.
func DefaultCatalog() Catalog {
	lines := make([]Line, len(defaultLines))
	copy(lines, defaultLines)
	return Catalog{
		ID:    DefaultCatalogID,
		Name:  "built-in optical emission-line catalog",
		Unit:  "nm",
		Lines: lines,
	}
}

// Validate checks a catalog's structural integrity: a declared unit, at
// least one line, and lines with unique non-empty identities and
// positive rest wavelengths.
func (c Catalog) Validate() error {
	if c.Unit == "" {
		return &Error{Kind: ErrMissingUnit, Field: "unit",
			Message: "the catalog must declare its wavelength unit"}
	}
	if len(c.Lines) == 0 {
		return &Error{Kind: ErrEmptyCatalog, Field: "lines",
			Message: "the catalog contains no lines"}
	}
	seen := make(map[string]bool, len(c.Lines))
	for i, ln := range c.Lines {
		if ln.ID == "" {
			return &Error{Kind: ErrMissingLineID, Field: "lines",
				Message: fmt.Sprintf("line %d: every line needs a stable identity", i)}
		}
		if seen[ln.ID] {
			return &Error{Kind: ErrDuplicateLineID, Field: "lines",
				Message: fmt.Sprintf("line identity %q appears more than once", ln.ID)}
		}
		seen[ln.ID] = true
		if ln.RestWavelength <= 0 || math.IsNaN(ln.RestWavelength) || math.IsInf(ln.RestWavelength, 0) {
			return &Error{Kind: ErrInvalidLineWavelength, Field: "lines",
				Message: fmt.Sprintf("line %q: rest wavelength must be positive, got %v", ln.ID, ln.RestWavelength)}
		}
	}
	return nil
}

// Snapshot is the frozen copy of a catalog taken at the start of one
// identification. Matching, the persisted report and any later replay
// all use the snapshot, so edits to the underlying catalog never leak
// into a run that has already begun.
type Snapshot struct {
	CatalogID string    `json:"catalog_id"`
	Name      string    `json:"name"`
	Unit      string    `json:"unit"`
	Lines     []Line    `json:"lines"`
	TakenAt   time.Time `json:"taken_at"`
}

// Snapshot deep-copies the catalog into an immutable per-run view.
func (c Catalog) Snapshot() Snapshot {
	lines := make([]Line, len(c.Lines))
	copy(lines, c.Lines)
	return Snapshot{
		CatalogID: c.ID,
		Name:      c.Name,
		Unit:      c.Unit,
		Lines:     lines,
		TakenAt:   time.Now().UTC(),
	}
}

// Catalog rebuilds a matchable catalog from the snapshot (again an
// independent copy).
func (s Snapshot) Catalog() Catalog {
	lines := make([]Line, len(s.Lines))
	copy(lines, s.Lines)
	return Catalog{ID: s.CatalogID, Name: s.Name, Unit: s.Unit, Lines: lines}
}
