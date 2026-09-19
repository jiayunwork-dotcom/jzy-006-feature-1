package service

import (
	"fmt"
	"math"
	"sync"
	"testing"

	"cosmocalc/internal/identify"
	"cosmocalc/internal/lines"
)

type memReportRepo struct {
	mu      sync.Mutex
	reports []*IdentifyReport
	nextID  int64
}

func newMemReportRepo() *memReportRepo { return &memReportRepo{nextID: 1} }

func (m *memReportRepo) SaveReport(rep *IdentifyReport) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rep.ID = m.nextID
	m.nextID++
	m.reports = append(m.reports, rep)
	return nil
}
func (m *memReportRepo) GetReport(id int64) (*IdentifyReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.reports {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, ErrReportMissing
}
func (m *memReportRepo) ListReports(limit, offset int) ([]*IdentifyReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*IdentifyReport(nil), m.reports...), nil
}

// ErrReportMissing is the service-package-local stand-in used by tests.
var ErrReportMissing = reportMissingError{}

type reportMissingError struct{}

func (reportMissingError) Error() string { return "report not found" }

func fptr(v float64) *float64 { return &v }

func newIdentifyService() (*IdentifyService, *lines.Registry) {
	reg := lines.NewRegistry(nil) // built-in table only; inline tables bypass repo
	return NewIdentifyService(reg, newMemReportRepo()), reg
}

// Full golden flow at the service layer: unique z=0.03, identities right,
// per-line redshifts right; with H0=70 the optional distance matches the
// preset example ~128.48 Mpc.
func TestIdentify_GoldenWithDistance(t *testing.T) {
	svc, _ := newIdentifyService()
	req := IdentifyRequest{
		WavelengthUnit: "nm",
		CatalogID:      lines.DefaultCatalogID,
		Peaks: []IdentifyPeak{
			{ObservedWavelength: fptr(675.9684)},
			{ObservedWavelength: fptr(500.7139)},
			{ObservedWavelength: fptr(515.721)},
		},
		HubbleConstant: fptr(70),
	}
	resp, err := svc.Identify(req)
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if resp.Status != identify.StatusUnique || resp.Candidate == nil {
		t.Fatalf("status=%v candidate=%+v", resp.Status, resp.Candidate)
	}
	if math.Abs(resp.Candidate.MeanRedshift-0.03) > 1e-9 {
		t.Fatalf("common z = %v, want 0.03", resp.Candidate.MeanRedshift)
	}
	wantIDs := map[float64]string{675.9684: "HI_HA", 500.7139: "HI_HB", 515.721: "OIII_5007"}
	for _, m := range resp.Candidate.Matches {
		if wantIDs[m.ObservedWavelength] != m.LineID {
			t.Fatalf("peak %v -> %v", m.ObservedWavelength, m.LineID)
		}
		if math.Abs(m.Redshift-0.03) > 1e-9 {
			t.Fatalf("per-line z for %v = %v, want 0.03", m.LineID, m.Redshift)
		}
	}
	if resp.Distance == nil {
		t.Fatal("H0=70 must attach the Hubble distance")
	}
	if math.Abs(resp.Distance.DistanceMpc-128.482) > 0.01 {
		t.Fatalf("distance = %v, want ~128.48 Mpc", resp.Distance.DistanceMpc)
	}
	if !resp.Distance.LinearRegime || resp.Distance.BeyondLinear {
		t.Fatalf("linear-regime flags wrong: %+v", resp.Distance)
	}
	if resp.DistanceError != nil {
		t.Fatalf("unexpected distance error: %+v", resp.DistanceError)
	}
	if resp.ID == 0 {
		t.Fatal("a persisted report must carry an id")
	}
}

// Single peak against the multi-line built-in table is ambiguous, not
// silently H-alpha at z = 0.03.
func TestIdentify_SinglePeakAmbiguous(t *testing.T) {
	svc, _ := newIdentifyService()
	resp, err := svc.Identify(IdentifyRequest{
		WavelengthUnit: "nm",
		CatalogID:      lines.DefaultCatalogID,
		Peaks:          []IdentifyPeak{{ObservedWavelength: fptr(675.9684)}},
	})
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if resp.Status != identify.StatusAmbiguous || len(resp.Candidates) < 2 {
		t.Fatalf("status=%v candidates=%d, want ambiguous with several candidates", resp.Status, len(resp.Candidates))
	}
}

// Modifying one peak yields a structured no-match but still a report.
func TestIdentify_NoMatchStillReport(t *testing.T) {
	svc, _ := newIdentifyService()
	resp, err := svc.Identify(IdentifyRequest{
		WavelengthUnit: "nm",
		CatalogID:      lines.DefaultCatalogID,
		Peaks: []IdentifyPeak{
			{ObservedWavelength: fptr(675.9684)},
			{ObservedWavelength: fptr(500.7139)},
			{ObservedWavelength: fptr(520)},
		},
	})
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if resp.Status != identify.StatusNoMatch {
		t.Fatalf("status = %v, want no_match", resp.Status)
	}
	if resp.ID == 0 {
		t.Fatal("a no-match attempt must still persist a retrievable report")
	}
}

// A uniquely identified blueshift is labelled; asking for its Hubble
// distance is refused with the same structured blueshift error the
// existing distance query uses.
func TestIdentify_BlueshiftDistanceRefused(t *testing.T) {
	svc, _ := newIdentifyService()
	resp, err := svc.Identify(IdentifyRequest{
		WavelengthUnit: "nm",
		Catalog: &lines.CatalogInput{WavelengthUnit: "nm", Lines: []lines.LineInput{
			{ID: "A", RestWavelength: 600},
		}},
		Peaks:          []IdentifyPeak{{ObservedWavelength: fptr(500)}},
		HubbleConstant: fptr(70),
	})
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if resp.Candidate.ShiftType != "blueshift" || resp.Candidate.MeanRedshift >= 0 {
		t.Fatalf("want labelled blueshift, got %+v", resp.Candidate)
	}
	if resp.Distance != nil {
		t.Fatal("blueshift must not carry a positive distance")
	}
	if resp.DistanceError == nil || resp.DistanceError.Type != "blueshift_distance" {
		t.Fatalf("distance error = %+v, want blueshift_distance", resp.DistanceError)
	}
}

// An inline table is the ONLY table for that run: built-in lines must not
// be mixed in.
func TestIdentify_InlineTableExclusive(t *testing.T) {
	svc, _ := newIdentifyService()
	// Two custom lines only; a peak that would match a built-in line must
	// not be identified against it.
	resp, err := svc.Identify(IdentifyRequest{
		WavelengthUnit: "nm",
		Catalog: &lines.CatalogInput{WavelengthUnit: "nm", Lines: []lines.LineInput{
			{ID: "X1", RestWavelength: 600},
			{ID: "X2", RestWavelength: 700},
		}},
		Peaks: []IdentifyPeak{{ObservedWavelength: fptr(618)}},
	})
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if resp.Status != identify.StatusAmbiguous {
		t.Fatalf("status = %v, want ambiguous", resp.Status)
	}
	for _, c := range resp.Candidates {
		id := c.Matches[0].LineID
		if id != "X1" && id != "X2" {
			t.Fatalf("inline run matched foreign built-in line %v", id)
		}
	}
	if resp.Snapshot.Source != lines.SourceInline {
		t.Fatalf("snapshot source = %v, want inline", resp.Snapshot.Source)
	}
	if len(resp.Snapshot.Lines) != 2 {
		t.Fatalf("snapshot must contain exactly the two inline lines, got %d", len(resp.Snapshot.Lines))
	}
}

// Unit disagreement must be refused before any matching, with a distinct
// structured error type.
func TestIdentify_UnitMismatchRejected(t *testing.T) {
	svc, _ := newIdentifyService()
	_, err := svc.Identify(IdentifyRequest{
		WavelengthUnit: "Angstrom",
		CatalogID:      lines.DefaultCatalogID, // nm
		Peaks:          []IdentifyPeak{{ObservedWavelength: fptr(6759.684)}},
	})
	le, ok := lines.AsError(err)
	if !ok || le.Kind != lines.ErrUnitMismatch {
		t.Fatalf("err = %v, want wavelength_unit_mismatch", err)
	}
}

// Pre-matching validation errors are distinct from the existing
// missing_field / non_positive_wavelength kinds.
func TestIdentify_ValidationErrorTypes(t *testing.T) {
	svc, _ := newIdentifyService()
	cases := []struct {
		name string
		req  IdentifyRequest
		want string
	}{
		{"empty peaks", IdentifyRequest{WavelengthUnit: "nm", CatalogID: lines.DefaultCatalogID}, "empty_peaks"},
		{"missing unit", IdentifyRequest{CatalogID: lines.DefaultCatalogID,
			Peaks: []IdentifyPeak{{ObservedWavelength: fptr(600)}}}, "missing_wavelength_unit"},
		{"missing peak wavelength", IdentifyRequest{WavelengthUnit: "nm", CatalogID: lines.DefaultCatalogID,
			Peaks: []IdentifyPeak{{}}}, "missing_peak_field"},
		{"non-positive peak", IdentifyRequest{WavelengthUnit: "nm", CatalogID: lines.DefaultCatalogID,
			Peaks: []IdentifyPeak{{ObservedWavelength: fptr(-1)}}}, "non_positive_observed_wavelength"},
		{"zero peak", IdentifyRequest{WavelengthUnit: "nm", CatalogID: lines.DefaultCatalogID,
			Peaks: []IdentifyPeak{{ObservedWavelength: fptr(0)}}}, "non_positive_observed_wavelength"},
		{"no catalog", IdentifyRequest{WavelengthUnit: "nm",
			Peaks: []IdentifyPeak{{ObservedWavelength: fptr(600)}}}, "missing_catalog"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Identify(tc.req)
			fe, ok := AsIdentifyFieldError(err)
			if !ok || fe.Type != tc.want {
				t.Fatalf("err = %v, want %s", err, tc.want)
			}
		})
	}
}

// Snapshot isolation: take the snapshot, edit the table, then run — the
// identification must still use the frozen wavelengths.
func TestIdentify_SnapshotUnaffectedByLaterTableEdit(t *testing.T) {
	repo := &mutableRepo{catalogs: map[string]*lines.Catalog{
		"mine": {ID: "mine", WavelengthUnit: "nm", Source: lines.SourceCustom, Lines: []lines.Line{
			{ID: "HI_HA", RestWavelength: 656.28, WavelengthUnit: "nm"},
			{ID: "HI_HB", RestWavelength: 486.13, WavelengthUnit: "nm"},
			{ID: "OIII_5007", RestWavelength: 500.7, WavelengthUnit: "nm"},
		}},
	}}
	svc := NewIdentifyService(lines.NewRegistry(repo), newMemReportRepo())

	req := IdentifyRequest{
		WavelengthUnit: "nm",
		CatalogID:      "mine",
		Peaks: []IdentifyPeak{
			{ObservedWavelength: fptr(675.9684)},
			{ObservedWavelength: fptr(500.7139)},
			{ObservedWavelength: fptr(515.721)},
		},
	}
	peaks, snap, _, err := svc.ValidateAndSnapshot(req)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// Edit the table BEFORE matching against the snapshot.
	if _, err := repo.UpdateLineWavelength("mine", "HI_HA", 999); err != nil {
		t.Fatalf("edit: %v", err)
	}

	res := identify.Run(snap, peaks)
	if res.Status != identify.StatusUnique {
		t.Fatalf("status = %v, want unique against frozen snapshot", res.Status)
	}
	if math.Abs(res.Candidates[0].MeanRedshift-0.03) > 1e-9 {
		t.Fatalf("z = %v, want 0.03 from frozen snapshot", res.Candidates[0].MeanRedshift)
	}
	ha, _ := snap.LineByID("HI_HA")
	if ha.RestWavelength != 656.28 {
		t.Fatalf("snapshot wavelength drifted: %v", ha.RestWavelength)
	}

	// A fresh identification must pick up the edited table and fail to
	// reproduce z = 0.03.
	_, snap2, _, err := svc.ValidateAndSnapshot(req)
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	ha2, _ := snap2.LineByID("HI_HA")
	if ha2.RestWavelength != 999 {
		t.Fatalf("next identification must see edited wavelength 999, got %v", ha2.RestWavelength)
	}
}

// Replay from a stored report reproduces identities and the common
// redshift even after the referenced table was changed.
func TestReplay_ReproducesAfterTableEdit(t *testing.T) {
	repo := &mutableRepo{catalogs: map[string]*lines.Catalog{
		"mine": {ID: "mine", WavelengthUnit: "nm", Source: lines.SourceCustom, Lines: append(
			[]lines.Line(nil), lines.BuiltinCatalog().Lines...)},
	}}
	svc := NewIdentifyService(lines.NewRegistry(repo), newMemReportRepo())

	req := IdentifyRequest{
		WavelengthUnit: "nm",
		CatalogID:      "mine",
		Peaks: []IdentifyPeak{
			{ObservedWavelength: fptr(675.9684)},
			{ObservedWavelength: fptr(500.7139)},
			{ObservedWavelength: fptr(515.721)},
		},
	}
	resp, err := svc.Identify(req)
	if err != nil || resp.Status != identify.StatusUnique {
		t.Fatalf("identify: status=%v err=%v", respStatus(resp), err)
	}
	rep, err := svc.GetReport(resp.ID)
	if err != nil {
		t.Fatalf("get report: %v", err)
	}

	// Corrupt every line afterwards.
	for _, l := range repo.catalogs["mine"].Lines {
		_, _ = repo.UpdateLineWavelength("mine", l.ID, 10)
	}

	replayed := svc.Replay(rep)
	if !replayed.Replayed {
		t.Fatal("replay response must be marked replayed")
	}
	if replayed.Status != identify.StatusUnique {
		t.Fatalf("replay status = %v, want unique", replayed.Status)
	}
	if math.Abs(replayed.Candidate.MeanRedshift-0.03) > 1e-9 {
		t.Fatalf("replay z = %v, want 0.03", replayed.Candidate.MeanRedshift)
	}
	wantIDs := map[float64]string{675.9684: "HI_HA", 500.7139: "HI_HB", 515.721: "OIII_5007"}
	for _, m := range replayed.Candidate.Matches {
		if wantIDs[m.ObservedWavelength] != m.LineID {
			t.Fatalf("replay peak %v -> %v", m.ObservedWavelength, m.LineID)
		}
	}
	if replayed.ID != rep.ID {
		t.Fatalf("replay must carry the original report id %d, got %d", rep.ID, replayed.ID)
	}
}

// Two concurrent identifications using two different inline tables must
// not exchange lines.
func TestIdentify_ConcurrentTablesDoNotBleed(t *testing.T) {
	svc, _ := newIdentifyService()
	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			// Table A: only lines near the golden peaks.
			resp, err := svc.Identify(IdentifyRequest{
				WavelengthUnit: "nm",
				Catalog: &lines.CatalogInput{WavelengthUnit: "nm", Lines: []lines.LineInput{
					{ID: "A_HA", RestWavelength: 656.28},
					{ID: "A_HB", RestWavelength: 486.13},
					{ID: "A_O3", RestWavelength: 500.7},
				}},
				Peaks: []IdentifyPeak{
					{ObservedWavelength: fptr(675.9684)},
					{ObservedWavelength: fptr(500.7139)},
					{ObservedWavelength: fptr(515.721)},
				},
			})
			if err != nil {
				errs <- err
				return
			}
			if resp.Status != identify.StatusUnique {
				errs <- fmt.Errorf("table A status %v", resp.Status)
				return
			}
			for _, m := range resp.Candidate.Matches {
				if len(m.LineID) < 2 || m.LineID[:2] != "A_" {
					errs <- fmt.Errorf("table A matched foreign line %v", m.LineID)
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			// Table B: two unrelated lines that make 675.9684 ambiguous.
			resp, err := svc.Identify(IdentifyRequest{
				WavelengthUnit: "nm",
				Catalog: &lines.CatalogInput{WavelengthUnit: "nm", Lines: []lines.LineInput{
					{ID: "B_X", RestWavelength: 600},
					{ID: "B_Y", RestWavelength: 670},
				}},
				Peaks: []IdentifyPeak{{ObservedWavelength: fptr(675.9684)}},
			})
			if err != nil {
				errs <- err
				return
			}
			if resp.Status != identify.StatusAmbiguous {
				errs <- fmt.Errorf("table B status %v, want ambiguous", resp.Status)
				return
			}
			for _, c := range resp.Candidates {
				id := c.Matches[0].LineID
				if len(id) < 2 || id[:2] != "B_" {
					errs <- fmt.Errorf("table B matched foreign line %v", id)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// mutableRepo is a lines.CatalogRepository whose catalogues can be edited
// concurrently; returned values share storage, which is deliberate here so
// the snapshot has something to defend against.
type mutableRepo struct {
	mu       sync.Mutex
	catalogs map[string]*lines.Catalog
}

func (r *mutableRepo) SaveCatalog(c *lines.Catalog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.catalogs[c.ID] = c.Clone()
	return nil
}
func (r *mutableRepo) GetCatalog(id string) (*lines.Catalog, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.catalogs[id]
	if !ok {
		return nil, &lines.Error{Kind: lines.ErrCatalogNotFound, Field: "catalog_id", Message: "missing"}
	}
	return c.Clone(), nil
}
func (r *mutableRepo) ListCatalogs() ([]*lines.Catalog, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*lines.Catalog, 0, len(r.catalogs))
	for _, c := range r.catalogs {
		out = append(out, c.Clone())
	}
	return out, nil
}
func (r *mutableRepo) UpdateLineWavelength(catalogID, lineID string, wavelength float64) (*lines.Catalog, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.catalogs[catalogID]
	if !ok {
		return nil, &lines.Error{Kind: lines.ErrCatalogNotFound, Field: "catalog_id", Message: "missing"}
	}
	for i := range c.Lines {
		if c.Lines[i].ID == lineID {
			c.Lines[i].RestWavelength = wavelength
			return c.Clone(), nil
		}
	}
	return nil, &lines.Error{Kind: lines.ErrLineNotFound, Field: "line_id", Message: "missing line"}
}

func respStatus(r *IdentifyResponse) identify.Status { return r.Status }
