package service

import (
	"errors"
	"fmt"
	"math"

	"cosmocalc/internal/calc"
	"cosmocalc/internal/identify"
	"cosmocalc/internal/lines"
)

// Identification error kinds. These are deliberately distinct from the
// calculation-core kinds (missing_field, non_positive_wavelength, ...)
// so callers can tell input-shape problems apart.
const (
	ErrIdentifyEmptyPeaks        = "empty_peaks"
	ErrIdentifyMissingPeakField  = "missing_peak_field"
	ErrIdentifyBadPeakWavelength = "non_positive_observed_wavelength"
	ErrIdentifyMissingUnit       = "missing_wavelength_unit"
	ErrIdentifyUnitMismatch      = "wavelength_unit_mismatch"
	ErrIdentifyNoCatalog         = "missing_catalog"
	ErrIdentifyTooManyPeaks      = "too_many_peaks"
)

// IdentifyFieldError is a structured pre-matching validation failure.
type IdentifyFieldError struct {
	Type    string
	Field   string
	Message string
}

func (e *IdentifyFieldError) Error() string { return e.Message }

// AsIdentifyFieldError unwraps err into an *IdentifyFieldError.
func AsIdentifyFieldError(err error) (*IdentifyFieldError, bool) {
	var fe *IdentifyFieldError
	if errors.As(err, &fe) {
		return fe, true
	}
	return nil, false
}

// MaxIdentifyPeaks caps combinatorial work; physical spectra never carry
// anywhere near this many unidentified peaks in one submission.
const MaxIdentifyPeaks = 50

// IdentifyService performs line identification against catalogues
// resolved from a registry and persists every attempt (unique, ambiguous
// or no-match) as its own report.
type IdentifyService struct {
	registry *lines.Registry
	reports  ReportRepository
}

// ReportRepository is the persistence port for identification reports.
type ReportRepository interface {
	SaveReport(rep *IdentifyReport) error
	GetReport(id int64) (*IdentifyReport, error)
	ListReports(limit, offset int) ([]*IdentifyReport, error)
}

// NewIdentifyService wires the registry and report store.
func NewIdentifyService(reg *lines.Registry, reports ReportRepository) *IdentifyService {
	return &IdentifyService{registry: reg, reports: reports}
}

// ValidateAndSnapshot performs ALL pre-matching checks and resolves the
// immutable snapshot used by this run. Mutating the underlying catalogue
// afterwards cannot affect the returned snapshot.
func (s *IdentifyService) ValidateAndSnapshot(req IdentifyRequest) ([]float64, *lines.Catalog, *float64, error) {
	if len(req.Peaks) == 0 {
		return nil, nil, nil, &IdentifyFieldError{Type: ErrIdentifyEmptyPeaks, Field: "peaks",
			Message: "peaks must contain at least one observed peak"}
	}
	if len(req.Peaks) > MaxIdentifyPeaks {
		return nil, nil, nil, &IdentifyFieldError{Type: ErrIdentifyTooManyPeaks, Field: "peaks",
			Message: fmt.Sprintf("at most %d peaks per identification, got %d", MaxIdentifyPeaks, len(req.Peaks))}
	}
	unit := lines.NormalizeUnit(req.WavelengthUnit)
	if unit == "" {
		return nil, nil, nil, &IdentifyFieldError{Type: ErrIdentifyMissingUnit, Field: "wavelength_unit",
			Message: "observed peaks must declare their wavelength unit"}
	}
	peaks := make([]float64, len(req.Peaks))
	for i, p := range req.Peaks {
		if p.ObservedWavelength == nil {
			return nil, nil, nil, &IdentifyFieldError{Type: ErrIdentifyMissingPeakField,
				Field:   fmt.Sprintf("peaks[%d].observed_wavelength", i),
				Message: fmt.Sprintf("peak #%d is missing observed_wavelength", i)}
		}
		w := *p.ObservedWavelength
		if !(w > 0) || math.IsNaN(w) || math.IsInf(w, 0) {
			return nil, nil, nil, &IdentifyFieldError{Type: ErrIdentifyBadPeakWavelength,
				Field:   fmt.Sprintf("peaks[%d].observed_wavelength", i),
				Message: fmt.Sprintf("observed wavelength for peak #%d must be positive and finite, got %v", i, w)}
		}
		peaks[i] = w
	}
	if req.HubbleConstant != nil && (*req.HubbleConstant <= 0 || math.IsNaN(*req.HubbleConstant) || math.IsInf(*req.HubbleConstant, 0)) {
		return nil, nil, nil, &calc.Error{Kind: calc.ErrNonPositiveHubble, Field: "hubble_constant",
			Message: fmt.Sprintf("Hubble constant must be positive (km/s/Mpc), got %v", *req.HubbleConstant)}
	}

	// Resolve the table: an inline table is exclusive to this run and must
	// never be mixed with the built-in lines.
	var catalog *lines.Catalog
	if req.Catalog != nil {
		c, err := lines.FromInput(*req.Catalog, lines.SourceInline)
		if err != nil {
			return nil, nil, nil, err
		}
		catalog = c
	} else if id := req.CatalogID; id != "" {
		c, err := s.registry.Get(id)
		if err != nil {
			return nil, nil, nil, err
		}
		catalog = c
	} else {
		return nil, nil, nil, &IdentifyFieldError{Type: ErrIdentifyNoCatalog, Field: "catalog_id",
			Message: "provide either a registered catalog_id or an inline catalog for this identification"}
	}

	// The declared units must agree literally; no silent conversion.
	if catalog.WavelengthUnit != unit {
		return nil, nil, nil, &lines.Error{Kind: lines.ErrUnitMismatch, Field: "wavelength_unit",
			Message: fmt.Sprintf("observed peaks declare unit %q but the catalog declares %q; identify wavelengths in their declared units", unit, catalog.WavelengthUnit)}
	}

	// THE snapshot for this run: an independent deep copy. Concurrent or
	// later edits to the table cannot touch it.
	return peaks, catalog.Clone(), req.HubbleConstant, nil
}

// Identify validates the request, snapshots the table, runs the pure
// matcher, optionally computes the Hubble distance for a unique
// non-blueshift result, and ALWAYS persists a report (validation errors
// are raised before matching and, by contract, are not reports).
func (s *IdentifyService) Identify(req IdentifyRequest) (*IdentifyResponse, error) {
	peaks, snapshot, h0, err := s.ValidateAndSnapshot(req)
	if err != nil {
		return nil, err
	}
	result := identify.Run(snapshot, peaks)

	rep := &IdentifyReport{
		Status:             result.Status,
		WavelengthUnit:     snapshot.WavelengthUnit,
		WindowKmS:          result.WindowKmS,
		ObservedWavelength: append([]float64(nil), peaks...),
		RequestedCatalogID: req.CatalogID,
		Snapshot:           snapshotOf(snapshot),
		Candidates:         append([]identify.Candidate(nil), result.Candidates...),
	}
	if h0 != nil {
		v := *h0
		rep.HubbleConstant = &v
	}

	if result.Status == identify.StatusUnique {
		s.attachDistance(rep, h0)
	}

	if err := s.reports.SaveReport(rep); err != nil {
		// Persistence failure must not hide the identification itself, but
		// the caller should see that the report id is missing.
		return responseFromReport(rep, false), nil
	}
	return responseFromReport(rep, false), nil
}

// attachDistance runs the EXISTING distance computation for the common
// redshift. A blueshift identification is labelled as such and its
// distance query is refused with the same structured error a direct
// /distance call would produce.
func (s *IdentifyService) attachDistance(rep *IdentifyReport, h0 *float64) {
	if h0 == nil {
		return
	}
	rep.DistanceResult = nil
	rep.DistanceError = nil
	z := rep.Candidates[0].MeanRedshift
	if calc.ClassifyShift(z) == calc.ShiftBlueshift {
		rep.DistanceError = &ReportDistanceError{
			Type:    string(calc.ErrBlueshiftDistance),
			Field:   "redshift",
			Message: "blueshift identified (z < 0): the object is approaching, no Hubble-flow distance is defined",
		}
		return
	}
	d, err := ComputeDistance(DistanceInput{
		Input:          Input{Redshift: &z},
		HubbleConstant: h0,
	})
	if err != nil {
		kind := "internal"
		field := "redshift"
		if ce, ok := AsCalcError(err); ok {
			kind = string(ce.Kind)
			field = ce.Field
		}
		rep.DistanceError = &ReportDistanceError{Type: kind, Field: field, Message: err.Error()}
		return
	}
	rep.DistanceResult = d
}

// Replay re-runs the identification EXACTLY as the stored snapshot
// records it. Even if the referenced catalogue has since been edited (or
// deleted), the identities and common redshift must reproduce the report.
// Replay never persists a new report and never reads the registry.
func (s *IdentifyService) Replay(rep *IdentifyReport) *IdentifyResponse {
	snap := catalogFromSnapshot(rep.Snapshot)
	result := identify.Run(snap, rep.ObservedWavelength)

	replayed := &IdentifyReport{
		Status:             result.Status,
		WavelengthUnit:     rep.WavelengthUnit,
		WindowKmS:          result.WindowKmS,
		ObservedWavelength: append([]float64(nil), rep.ObservedWavelength...),
		RequestedCatalogID: rep.RequestedCatalogID,
		HubbleConstant:     rep.HubbleConstant,
		Snapshot:           rep.Snapshot,
		Candidates:         result.Candidates,
	}
	if result.Status == identify.StatusUnique && rep.HubbleConstant != nil {
		s.attachDistance(replayed, rep.HubbleConstant)
	}
	resp := responseFromReport(replayed, true)
	// Replay is the same report: expose the original identity/timestamp.
	resp.ID = rep.ID
	resp.CreatedAt = rep.CreatedAt
	return resp
}

// GetReport retrieves a stored report by id.
func (s *IdentifyService) GetReport(id int64) (*IdentifyReport, error) {
	return s.reports.GetReport(id)
}

// ListReports returns recent reports newest-first.
func (s *IdentifyService) ListReports(limit, offset int) ([]*IdentifyReport, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.reports.ListReports(limit, maxInt(offset, 0))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// snapshotOf copies a resolved catalogue into the persisted snapshot
// shape.
func snapshotOf(c *lines.Catalog) SnapshotCatalog {
	snap := SnapshotCatalog{
		ID:             c.ID,
		Name:           c.Name,
		WavelengthUnit: c.WavelengthUnit,
		Source:         c.Source,
		Lines:          make([]SnapshotLine, len(c.Lines)),
	}
	for i, l := range c.Lines {
		snap.Lines[i] = SnapshotLine{
			ID:             l.ID,
			Label:          l.Label,
			RestWavelength: l.RestWavelength,
			WavelengthUnit: l.WavelengthUnit,
		}
	}
	return snap
}

// catalogFromSnapshot rebuilds an immutable catalogue purely from a
// stored snapshot, so replay needs no registry access.
func catalogFromSnapshot(snap SnapshotCatalog) *lines.Catalog {
	c := &lines.Catalog{
		ID:             snap.ID,
		Name:           snap.Name,
		WavelengthUnit: snap.WavelengthUnit,
		Source:         snap.Source,
		Lines:          make([]lines.Line, len(snap.Lines)),
	}
	for i, l := range snap.Lines {
		c.Lines[i] = lines.Line{
			ID:             l.ID,
			Label:          l.Label,
			RestWavelength: l.RestWavelength,
			WavelengthUnit: l.WavelengthUnit,
		}
	}
	return c
}

// responseFromReport projects a report into its external view.
func responseFromReport(rep *IdentifyReport, replayed bool) *IdentifyResponse {
	resp := &IdentifyResponse{
		ID:                 rep.ID,
		CreatedAt:          rep.CreatedAt,
		Status:             rep.Status,
		WavelengthUnit:     rep.WavelengthUnit,
		WindowKmS:          rep.WindowKmS,
		ObservedWavelength: append([]float64(nil), rep.ObservedWavelength...),
		Snapshot:           rep.Snapshot,
		Distance:           rep.DistanceResult,
		DistanceError:      rep.DistanceError,
		Replayed:           replayed,
	}
	if rep.Status == identify.StatusUnique && len(rep.Candidates) == 1 {
		c := rep.Candidates[0]
		resp.Candidate = &c
	} else {
		resp.Candidates = append([]identify.Candidate(nil), rep.Candidates...)
	}
	return resp
}
