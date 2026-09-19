package service

import (
	"encoding/json"
	"math"
	"testing"

	"cosmocalc/internal/calc"
	"cosmocalc/internal/lines"
)

func benchmarkInput() IdentifyInput {
	return IdentifyInput{
		Peaks:          []float64{675.9684, 500.7139, 515.721},
		WavelengthUnit: "nm",
	}
}

// The benchmark trio identifies uniquely at z = 0.03 with the expected
// identities, redshift classification and linear-relation velocity.
func TestIdentify_Benchmark(t *testing.T) {
	out, err := Identify(lines.DefaultCatalog(), benchmarkInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Status != StatusIdentified {
		t.Fatalf("status = %v, want identified", out.Status)
	}
	if math.Abs(out.Candidate.CommonRedshift-0.03) > 1e-9 {
		t.Fatalf("common redshift = %v, want 0.03", out.Candidate.CommonRedshift)
	}
	if out.ShiftType != calc.ShiftRedshift {
		t.Fatalf("shift type = %v, want redshift", out.ShiftType)
	}
	if math.Abs(out.VelocityKmS-8993.77374) > 0.01 {
		t.Fatalf("velocity = %v, want ~8993.77 km/s", out.VelocityKmS)
	}
	want := []string{"Hα", "Hβ", "[OIII]"}
	for i, m := range out.Candidate.Matches {
		if m.LineID != want[i] {
			t.Fatalf("match %d: identity = %q, want %q", i, m.LineID, want[i])
		}
	}
	// The snapshot carries exactly the lines that were matched against.
	if len(out.Snapshot.Lines) != len(lines.DefaultCatalog().Lines) {
		t.Fatalf("snapshot holds %d lines, want the full catalog", len(out.Snapshot.Lines))
	}
	if out.Snapshot.CatalogID != lines.DefaultCatalogID {
		t.Fatalf("snapshot catalog = %q, want default", out.Snapshot.CatalogID)
	}
}

// With H0 = 70 the identified redshift must yield the same distance and
// linear-regime verdict as the existing distance computation.
func TestIdentify_DistanceMatchesExistingComputation(t *testing.T) {
	in := benchmarkInput()
	h0 := 70.0
	in.HubbleConstant = &h0
	out, err := Identify(lines.DefaultCatalog(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Distance == nil {
		t.Fatal("distance must be computed when hubble_constant is given")
	}
	if math.Abs(out.Distance.DistanceMpc-128.482) > 0.01 {
		t.Fatalf("distance = %v Mpc, want ~128.48", out.Distance.DistanceMpc)
	}
	if !out.Distance.LinearRegime || out.Distance.BeyondLinear {
		t.Fatalf("z=0.03 must be inside the linear regime: %+v", out.Distance)
	}
	// Cross-check against the existing distance path directly.
	z := 0.03
	ref, err := ComputeDistance(DistanceInput{Input: Input{Redshift: &z}, HubbleConstant: &h0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(out.Distance.DistanceMpc-ref.DistanceMpc) > 1e-9 {
		t.Fatalf("identify distance %v != existing distance %v", out.Distance.DistanceMpc, ref.DistanceMpc)
	}
	if out.Distance.LinearRegime != ref.LinearRegime || out.Distance.BeyondLinear != ref.BeyondLinear {
		t.Fatal("linear-regime verdict must match the existing distance computation")
	}
}

// Pre-matching rejections use error types distinct from the existing
// missing_field / non_positive_wavelength computation errors.
func TestIdentify_ValidationErrors(t *testing.T) {
	h0Neg := -1.0
	cases := []struct {
		name     string
		in       IdentifyInput
		cat      lines.Catalog
		wantType string
	}{
		{"empty peaks", IdentifyInput{Peaks: []float64{}, WavelengthUnit: "nm"}, lines.DefaultCatalog(), ErrEmptyPeaks},
		{"nil peaks", IdentifyInput{WavelengthUnit: "nm"}, lines.DefaultCatalog(), ErrEmptyPeaks},
		{"missing unit", IdentifyInput{Peaks: []float64{500}}, lines.DefaultCatalog(), ErrMissingUnit},
		{"negative peak", IdentifyInput{Peaks: []float64{-5}, WavelengthUnit: "nm"}, lines.DefaultCatalog(), ErrInvalidPeakWavelength},
		{"zero peak", IdentifyInput{Peaks: []float64{0}, WavelengthUnit: "nm"}, lines.DefaultCatalog(), ErrInvalidPeakWavelength},
		{"unit mismatch", IdentifyInput{Peaks: []float64{500}, WavelengthUnit: "angstrom"}, lines.DefaultCatalog(), ErrUnitMismatch},
		{"bad hubble", IdentifyInput{Peaks: []float64{500}, WavelengthUnit: "nm", HubbleConstant: &h0Neg},
			lines.DefaultCatalog(), string(calc.ErrNonPositiveHubble)},
	}
	for _, c := range cases {
		_, err := Identify(c.cat, c.in)
		if err == nil {
			t.Errorf("%s: expected error", c.name)
			continue
		}
		var gotType string
		if ie, ok := AsIdentifyError(err); ok {
			gotType = ie.Type
		} else if ce, ok := AsCalcError(err); ok {
			gotType = string(ce.Kind)
		}
		if gotType != c.wantType {
			t.Errorf("%s: error type = %q, want %q (err=%v)", c.name, gotType, c.wantType, err)
		}
		if gotType == "missing_field" || gotType == "non_positive_wavelength" {
			t.Errorf("%s: type %q collides with existing computation errors", c.name, gotType)
		}
	}
}

// Catalog-side structural problems surface as typed lines errors.
func TestIdentify_CatalogValidationErrors(t *testing.T) {
	in := IdentifyInput{Peaks: []float64{500}, WavelengthUnit: "nm"}
	_, err := Identify(lines.Catalog{ID: "inline", Unit: "nm"}, in)
	le, ok := err.(*lines.Error)
	if !ok || le.Kind != lines.ErrEmptyCatalog {
		t.Fatalf("empty catalog: err = %v, want empty_catalog", err)
	}
	_, err = Identify(lines.Catalog{ID: "inline", Unit: "nm", Lines: []lines.Line{{ID: "A", RestWavelength: -1}}}, in)
	le, ok = err.(*lines.Error)
	if !ok || le.Kind != lines.ErrInvalidLineWavelength {
		t.Fatalf("bad line wavelength: err = %v, want invalid_line_wavelength", err)
	}
}

// No consistent assignment → typed no-match error, but the outcome
// (with snapshot) still exists so a report can be persisted.
func TestIdentify_NoMatch(t *testing.T) {
	in := IdentifyInput{Peaks: []float64{675.9684, 520, 515.721}, WavelengthUnit: "nm"}
	out, err := Identify(lines.DefaultCatalog(), in)
	if err == nil {
		t.Fatal("expected a no-match error")
	}
	ie, ok := AsIdentifyError(err)
	if !ok || ie.Type != ErrNoConsistentRedshift {
		t.Fatalf("error type = %v, want no_consistent_redshift", err)
	}
	if out == nil || out.Status != StatusNoMatch {
		t.Fatalf("outcome = %+v, want no_match outcome for the report", out)
	}
	if len(out.Snapshot.Lines) == 0 {
		t.Fatal("no-match outcome must still carry the snapshot")
	}
}

// A single peak against the default catalog is ambiguous.
func TestIdentify_Ambiguous(t *testing.T) {
	in := IdentifyInput{Peaks: []float64{675.9684}, WavelengthUnit: "nm"}
	out, err := Identify(lines.DefaultCatalog(), in)
	if err != nil {
		t.Fatalf("ambiguity is not an error: %v", err)
	}
	if out.Status != StatusAmbiguous {
		t.Fatalf("status = %v, want ambiguous", out.Status)
	}
	if len(out.Candidates) < 3 {
		t.Fatalf("got %d candidates, want several", len(out.Candidates))
	}
}

// A negative common redshift is marked as a blueshift; requesting a
// distance on it is rejected with the standard blueshift rule, while
// the identification outcome itself stands.
func TestIdentify_BlueshiftDistanceRejected(t *testing.T) {
	cat := lines.Catalog{ID: "inline", Unit: "nm", Lines: []lines.Line{
		{ID: "A", RestWavelength: 500},
		{ID: "B", RestWavelength: 600},
		{ID: "C", RestWavelength: 700},
	}}
	in := IdentifyInput{Peaks: []float64{485, 582, 679}, WavelengthUnit: "nm"}

	out, err := Identify(cat, in)
	if err != nil {
		t.Fatalf("blueshift identification must succeed: %v", err)
	}
	if out.ShiftType != calc.ShiftBlueshift {
		t.Fatalf("shift type = %v, want blueshift", out.ShiftType)
	}

	h0 := 70.0
	in.HubbleConstant = &h0
	out, err = Identify(cat, in)
	if err == nil {
		t.Fatal("distance on a blueshift must be rejected")
	}
	ce, ok := AsCalcError(err)
	if !ok || ce.Kind != calc.ErrBlueshiftDistance {
		t.Fatalf("error = %v, want blueshift_distance", err)
	}
	if out == nil || out.Status != StatusIdentified || out.Distance != nil {
		t.Fatalf("outcome = %+v, want identified outcome without distance", out)
	}
}

// An inline custom catalog is used exclusively — the built-in lines
// are never blended in.
func TestIdentify_InlineCatalogOnly(t *testing.T) {
	cat := lines.Catalog{ID: "inline", Unit: "nm", Lines: []lines.Line{
		{ID: "X", RestWavelength: 1000},
		{ID: "Y", RestWavelength: 2000},
	}}
	// 656.28 nm would be z=0 on built-in Hα; here it must only ever
	// match X or Y.
	out, err := Identify(cat, IdentifyInput{Peaks: []float64{656.28}, WavelengthUnit: "nm"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Status != StatusAmbiguous || len(out.Candidates) != 2 {
		t.Fatalf("outcome = %+v, want exactly the 2 inline candidates", out)
	}
	for _, c := range out.Candidates {
		if id := c.Matches[0].LineID; id != "X" && id != "Y" {
			t.Fatalf("candidate used line %q — built-in catalog leaked in", id)
		}
	}
}

// Snapshot isolation at the orchestration level: a catalog edited
// after the snapshot does not affect a run built on that snapshot,
// while a fresh run sees the updated table.
func TestIdentify_SnapshotIsolationAcrossRuns(t *testing.T) {
	peaks := []float64{515, 618} // z = 0.03 on A@500, B@600
	catV1 := lines.Catalog{ID: "1", Unit: "nm", Lines: []lines.Line{
		{ID: "A", RestWavelength: 500},
		{ID: "B", RestWavelength: 600},
	}}
	snap := catV1.Snapshot()

	// The table is edited after the snapshot was taken.
	catV2 := catV1
	catV2.Lines = []lines.Line{{ID: "A", RestWavelength: 505}, {ID: "B", RestWavelength: 600}}

	in := IdentifyInput{Peaks: peaks, WavelengthUnit: "nm"}
	out, err := Identify(snap.Catalog(), in)
	if err != nil || out.Status != StatusIdentified {
		t.Fatalf("snapshot run must still identify with the old table: out=%+v err=%v", out, err)
	}
	if math.Abs(out.Candidate.CommonRedshift-0.03) > 1e-9 {
		t.Fatalf("snapshot run z = %v, want 0.03", out.Candidate.CommonRedshift)
	}
	if out.Candidate.Matches[0].RestWavelength != 500 {
		t.Fatalf("snapshot run used rest %v, want the snapshotted 500", out.Candidate.Matches[0].RestWavelength)
	}

	out2, err := Identify(catV2, in)
	if err == nil || out2.Status != StatusNoMatch {
		t.Fatalf("fresh run must use the edited table and fail: out=%+v err=%v", out2, err)
	}
}

// catalog_id round-trips through the request JSON so reports keep the
// original reference.
func TestIdentifyInput_JSONRoundTrip(t *testing.T) {
	in := IdentifyInput{
		Peaks:          []float64{515, 618},
		WavelengthUnit: "nm",
		CatalogID:      json.RawMessage(`"default"`),
	}
	buf, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back IdentifyInput
	if err := json.Unmarshal(buf, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(back.CatalogID) != `"default"` || len(back.Peaks) != 2 || back.WavelengthUnit != "nm" {
		t.Fatalf("round trip changed the request: %+v", back)
	}
}
