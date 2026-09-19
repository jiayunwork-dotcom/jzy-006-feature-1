// Package lines owns rest-wavelength line catalogues: the service's
// built-in optical emission-line table, caller-registered custom tables,
// their validation and their per-identification snapshots.
//
// Every catalogue declares ONE wavelength unit for all of its lines;
// observed peaks must declare the same unit, otherwise identification is
// refused (the comparison is never performed after a silent conversion).
package lines

import (
	"fmt"
	"sort"
	"strings"
)

// DefaultCatalogID is the stable identity of the built-in optical
// emission-line table shipped with the service.
const DefaultCatalogID = "builtin_optical_emission"

// Line is one laboratory rest-wavelength entry. ID is a stable identity
// that survives rest-wavelength corrections; wavelength values alone are
// never used as identities.
type Line struct {
	ID             string  `json:"id"`
	Label          string  `json:"label,omitempty"`
	RestWavelength float64 `json:"rest_wavelength"`
	// WavelengthUnit is declared on the line for self-description; within
	// a catalogue every line shares the catalogue's unit.
	WavelengthUnit string `json:"wavelength_unit"`
}

// LineInput is one line as submitted by a caller when registering a table.
type LineInput struct {
	ID             string  `json:"id"`
	Label          string  `json:"label,omitempty"`
	RestWavelength float64 `json:"rest_wavelength"`
}

// CatalogInput is a caller-submitted custom table.
type CatalogInput struct {
	ID             string      `json:"id"`
	Name           string      `json:"name,omitempty"`
	WavelengthUnit string      `json:"wavelength_unit"`
	Lines          []LineInput `json:"lines"`
}

// Source says where a resolved catalogue came from.
type Source string

const (
	// SourceBuiltin is the service's own shipped table. It is read-only.
	SourceBuiltin Source = "builtin"
	// SourceCustom is a table a caller registered and that persists.
	SourceCustom Source = "custom"
	// SourceInline is a one-shot table supplied inside an identify request;
	// only that identification may use it.
	SourceInline Source = "inline"
)

// Catalog is a fully resolved, validated table.
type Catalog struct {
	ID             string `json:"id"`
	Name           string `json:"name,omitempty"`
	WavelengthUnit string `json:"wavelength_unit"`
	Source         Source `json:"source"`
	Lines          []Line `json:"lines"`
}

// builtinOpticalEmissionLines is the shipped laboratory table of common
// optical emission lines (vacuum/air convention as published; the ratio
// arithmetic only cares that rest and observed use the same convention).
// It deliberately contains several lines around H-alpha so that a single
// observed peak matches more than one of them at different redshifts.
var builtinOpticalEmissionLines = []Line{
	{ID: "HI_HA", Label: "H I H-alpha", RestWavelength: 656.28},
	{ID: "HI_HB", Label: "H I H-beta", RestWavelength: 486.13},
	{ID: "HI_HG", Label: "H I H-gamma", RestWavelength: 434.05},
	{ID: "HI_HD", Label: "H I H-delta", RestWavelength: 410.17},
	{ID: "HI_HE", Label: "H I H-epsilon", RestWavelength: 397.01},
	{ID: "HI_H8", Label: "H I H-zeta", RestWavelength: 388.91},
	{ID: "OII_3726", Label: "[O II] 372.6", RestWavelength: 372.61},
	{ID: "OII_3729", Label: "[O II] 372.9", RestWavelength: 372.89},
	{ID: "NEIII_3869", Label: "[Ne III] 386.9", RestWavelength: 386.88},
	{ID: "NEIII_3967", Label: "[Ne III] 396.7", RestWavelength: 396.75},
	{ID: "OIII_4959", Label: "[O III] 495.9", RestWavelength: 495.89},
	{ID: "OIII_5007", Label: "[O III] 500.7", RestWavelength: 500.7},
	{ID: "OI_6300", Label: "[O I] 630.0", RestWavelength: 630.03},
	{ID: "OI_6364", Label: "[O I] 636.4", RestWavelength: 636.38},
	{ID: "NII_6548", Label: "[N II] 654.8", RestWavelength: 654.80},
	{ID: "NII_6584", Label: "[N II] 658.3", RestWavelength: 658.34},
	{ID: "HEI_5876", Label: "He I 587.6", RestWavelength: 587.56},
	{ID: "HEI_6678", Label: "He I 667.8", RestWavelength: 667.82},
	{ID: "HEI_7065", Label: "He I 706.5", RestWavelength: 706.52},
	{ID: "SII_6716", Label: "[S II] 671.6", RestWavelength: 671.64},
	{ID: "SII_6731", Label: "[S II] 673.1", RestWavelength: 673.08},
}

// BuiltinCatalog returns a fresh deep copy of the shipped optical
// emission-line table in nanometres.
func BuiltinCatalog() *Catalog {
	lines := make([]Line, len(builtinOpticalEmissionLines))
	for i, l := range builtinOpticalEmissionLines {
		l.WavelengthUnit = "nm"
		lines[i] = l
	}
	return &Catalog{
		ID:             DefaultCatalogID,
		Name:           "Built-in common optical emission lines",
		WavelengthUnit: "nm",
		Source:         SourceBuiltin,
		Lines:          lines,
	}
}

// NormalizeUnit trims surrounding whitespace; declarations are compared
// case-sensitively (nm vs Angstrom must be declared explicitly, never
// guessed).
func NormalizeUnit(unit string) string { return strings.TrimSpace(unit) }

// ValidateLines checks a submitted line set: every line needs a stable ID,
// a positive finite wavelength, IDs must be unique, and the set must be
// non-empty when it describes a table.
func ValidateLines(lines []LineInput, unit string) error {
	if len(lines) == 0 {
		return &Error{Kind: ErrEmptyCatalog, Field: "lines",
			Message: "line catalog must contain at least one line"}
	}
	seen := make(map[string]bool, len(lines))
	for i, l := range lines {
		field := fmt.Sprintf("lines[%d].id", i)
		if strings.TrimSpace(l.ID) == "" {
			return &Error{Kind: ErrMissingLineID, Field: field,
				Message: fmt.Sprintf("line #%d is missing its stable id", i)}
		}
		if seen[l.ID] {
			return &Error{Kind: ErrDuplicateLineID, Field: field,
				Message: fmt.Sprintf("duplicate line id %q in catalog", l.ID)}
		}
		seen[l.ID] = true
		wfield := fmt.Sprintf("lines[%d].rest_wavelength", i)
		if !isPositiveFinite(l.RestWavelength) {
			return &Error{Kind: ErrNonPositiveLineWavelength, Field: wfield,
				Message: fmt.Sprintf("rest wavelength for line %q must be positive and finite, got %v", l.ID, l.RestWavelength)}
		}
	}
	return nil
}

// FromInput validates a submitted table and turns it into a Catalog. The
// source is tagged by the caller (custom vs inline).
func FromInput(in CatalogInput, src Source) (*Catalog, error) {
	id := strings.TrimSpace(in.ID)
	if src == SourceCustom && id == "" {
		return nil, &Error{Kind: ErrMissingCatalogID, Field: "id",
			Message: "a registered catalog requires a stable id"}
	}
	if id == "" {
		id = "inline"
	}
	unit := NormalizeUnit(in.WavelengthUnit)
	if unit == "" {
		return nil, &Error{Kind: ErrMissingWavelengthUnit, Field: "wavelength_unit",
			Message: "catalog must declare the wavelength unit shared by all lines"}
	}
	if err := ValidateLines(in.Lines, unit); err != nil {
		return nil, err
	}
	out := &Catalog{
		ID:             id,
		Name:           strings.TrimSpace(in.Name),
		WavelengthUnit: unit,
		Source:         src,
		Lines:          make([]Line, len(in.Lines)),
	}
	for i, l := range in.Lines {
		out.Lines[i] = Line{
			ID:             l.ID,
			Label:          strings.TrimSpace(l.Label),
			RestWavelength: l.RestWavelength,
			WavelengthUnit: unit,
		}
	}
	sort.SliceStable(out.Lines, func(i, j int) bool { return out.Lines[i].ID < out.Lines[j].ID })
	return out, nil
}

// Clone returns an independent deep copy so callers can keep using a
// snapshot after the underlying table has been edited.
func (c *Catalog) Clone() *Catalog {
	if c == nil {
		return nil
	}
	cp := *c
	cp.Lines = append([]Line(nil), c.Lines...)
	return &cp
}

// LineByID looks a line up by its stable identity.
func (c *Catalog) LineByID(id string) (Line, bool) {
	for _, l := range c.Lines {
		if l.ID == id {
			return l, true
		}
	}
	return Line{}, false
}
