// Spectral-line identification orchestration: validate the request,
// freeze the catalog into a per-run snapshot, run the pure matching
// engine, and apply the outcome rules (unique / ambiguous / no-match,
// blueshift marking, optional Hubble-law distance).
package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"cosmocalc/internal/calc"
	"cosmocalc/internal/lines"
)

// IdentifyStatus labels the outcome of one identification run.
type IdentifyStatus string

const (
	StatusIdentified IdentifyStatus = "identified"
	StatusAmbiguous  IdentifyStatus = "ambiguous"
	StatusNoMatch    IdentifyStatus = "no_match"
)

// Structured identification error types. They are deliberately
// distinct from the existing "missing_field" and
// "non_positive_wavelength" computation errors.
const (
	ErrEmptyPeaks            = "empty_peaks"
	ErrMissingUnit           = "missing_unit"
	ErrInvalidPeakWavelength = "invalid_peak_wavelength"
	ErrUnitMismatch          = "unit_mismatch"
	ErrNoConsistentRedshift  = "no_consistent_redshift"
	ErrCatalogNotFound       = "catalog_not_found"
	ErrReportNotFound        = "report_not_found"
	ErrImmutableCatalog      = "immutable_catalog"
	ErrInvalidInput          = "invalid_input"
)

// IdentifyError is a typed identification-layer error.
type IdentifyError struct {
	Type    string
	Field   string
	Message string
}

func (e *IdentifyError) Error() string { return e.Message }

// AsIdentifyError unwraps err into an *IdentifyError if possible.
func AsIdentifyError(err error) (*IdentifyError, bool) {
	var ie *IdentifyError
	if errors.As(err, &ie) {
		return ie, true
	}
	return nil, false
}

// IdentifyInput is one identification request: the observed peaks of a
// single object (wavelengths only — no rest wavelengths, no redshift),
// the unit they are declared in, an optional catalog reference
// (catalog_id naming a registered catalog or "default", or an inline
// catalog used for this run only), and an optional Hubble constant to
// also derive a Hubble-law distance from the identified redshift.
type IdentifyInput struct {
	Peaks          []float64       `json:"peaks"`
	WavelengthUnit string          `json:"wavelength_unit"`
	CatalogID      json.RawMessage `json:"catalog_id,omitempty"`
	Catalog        *lines.Catalog  `json:"catalog,omitempty"`
	HubbleConstant *float64        `json:"hubble_constant,omitempty"`
}

// IdentifyOutcome carries everything a completed run produced. It is
// non-nil whenever matching actually ran (including no-match and
// blueshift-distance rejections), so the caller can persist a report
// carrying the snapshot; purely structural rejections (validation)
// happen before matching and yield a nil outcome.
type IdentifyOutcome struct {
	Status              IdentifyStatus
	Snapshot            lines.Snapshot
	Candidate           *lines.Candidate // set when Status == identified
	Candidates          []lines.Candidate
	CandidatesTruncated bool
	ShiftType           calc.ShiftType
	VelocityKmS         float64 // recession velocity of the common redshift (linear relation)
	Distance            *DistanceResult
}

// ValidateIdentifyInput rejects structurally invalid requests before
// any matching happens: empty peak list, missing unit, non-positive
// peak wavelengths, and a non-positive Hubble constant.
func ValidateIdentifyInput(in IdentifyInput) error {
	if len(in.Peaks) == 0 {
		return &IdentifyError{Type: ErrEmptyPeaks, Field: "peaks",
			Message: "peaks must contain at least one observed wavelength"}
	}
	if in.WavelengthUnit == "" {
		return &IdentifyError{Type: ErrMissingUnit, Field: "wavelength_unit",
			Message: "wavelength_unit is required; peaks are interpreted strictly in the declared unit"}
	}
	for j, p := range in.Peaks {
		if p <= 0 || math.IsNaN(p) || math.IsInf(p, 0) {
			return &IdentifyError{Type: ErrInvalidPeakWavelength, Field: "peaks",
				Message: fmt.Sprintf("peak %d: observed wavelength must be positive, got %v", j, p)}
		}
	}
	if in.HubbleConstant != nil {
		if *in.HubbleConstant <= 0 || math.IsNaN(*in.HubbleConstant) || math.IsInf(*in.HubbleConstant, 0) {
			return &calc.Error{Kind: calc.ErrNonPositiveHubble, Field: "hubble_constant",
				Message: fmt.Sprintf("Hubble constant must be positive (km/s/Mpc), got %v", *in.HubbleConstant)}
		}
	}
	return nil
}

// Identify runs one identification against the given catalog. The
// catalog is snapshotted up front and only the snapshot is matched
// against, so a concurrent edit of the source table cannot affect this
// run. The outcome rules:
//
//   - exactly one consistent assignment  → identified (unique common z)
//   - more than one                      → ambiguous, all candidates listed
//   - none                               → no_match, typed error (no fudged redshift)
//
// A negative common redshift is reported as a blueshift; if a distance
// was also requested it is rejected with the standard blueshift rule.
func Identify(cat lines.Catalog, in IdentifyInput) (*IdentifyOutcome, error) {
	if err := ValidateIdentifyInput(in); err != nil {
		return nil, err
	}
	if err := cat.Validate(); err != nil {
		return nil, err
	}
	if cat.Unit != in.WavelengthUnit {
		return nil, &IdentifyError{Type: ErrUnitMismatch, Field: "wavelength_unit",
			Message: fmt.Sprintf("peaks are declared in %q but catalog %q declares %q; units must match (no conversion is performed)",
				in.WavelengthUnit, cat.ID, cat.Unit)}
	}

	snapshot := cat.Snapshot()
	candidates, truncated := lines.Identify(in.Peaks, snapshot.Catalog())
	out := &IdentifyOutcome{Snapshot: snapshot}

	switch len(candidates) {
	case 0:
		out.Status = StatusNoMatch
		return out, &IdentifyError{Type: ErrNoConsistentRedshift, Field: "peaks",
			Message: "no assignment of all peaks to distinct catalog lines agrees within the 200 km/s velocity window"}
	case 1:
		c := candidates[0]
		out.Status = StatusIdentified
		out.Candidate = &c
		out.ShiftType = calc.ClassifyShift(c.CommonRedshift)
		v, err := calc.VelocityFromRedshift(c.CommonRedshift, false)
		if err != nil {
			return out, err
		}
		out.VelocityKmS = v
		if in.HubbleConstant != nil {
			if out.ShiftType == calc.ShiftBlueshift {
				return out, &calc.Error{Kind: calc.ErrBlueshiftDistance, Field: "redshift",
					Message: "blueshift detected (z < 0): the object is approaching, no Hubble-flow distance is defined"}
			}
			z := c.CommonRedshift
			d, err := ComputeDistance(DistanceInput{
				Input:          Input{Redshift: &z},
				HubbleConstant: in.HubbleConstant,
			})
			if err != nil {
				return out, err
			}
			out.Distance = d
		}
		return out, nil
	default:
		out.Status = StatusAmbiguous
		out.Candidates = candidates
		out.CandidatesTruncated = truncated
		return out, nil
	}
}
