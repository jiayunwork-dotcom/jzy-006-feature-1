package lines

import (
	"math"
	"testing"

	"cosmocalc/internal/calc"
)

const zTol = 1e-9

// The benchmark: the three preset-example peaks must identify uniquely
// as Hα, Hβ and [OIII] at common redshift 0.03.
func TestIdentify_BenchmarkTrioUnique(t *testing.T) {
	peaks := []float64{675.9684, 500.7139, 515.721}
	cands, truncated := Identify(peaks, DefaultCatalog())
	if truncated {
		t.Fatal("enumeration must not be truncated for the default catalog")
	}
	if len(cands) != 1 {
		t.Fatalf("got %d candidates, want exactly 1: %+v", len(cands), cands)
	}
	c := cands[0]
	if math.Abs(c.CommonRedshift-0.03) > zTol {
		t.Fatalf("common redshift = %v, want 0.03", c.CommonRedshift)
	}
	wantIDs := []string{"Hα", "Hβ", "[OIII]"}
	wantRest := []float64{656.28, 486.13, 500.7}
	if len(c.Matches) != 3 {
		t.Fatalf("got %d matches, want 3", len(c.Matches))
	}
	for i, m := range c.Matches {
		if m.LineID != wantIDs[i] {
			t.Errorf("peak %d: identity = %q, want %q", i, m.LineID, wantIDs[i])
		}
		if m.RestWavelength != wantRest[i] {
			t.Errorf("peak %d: rest wavelength = %v, want %v", i, m.RestWavelength, wantRest[i])
		}
		if math.Abs(m.Redshift-0.03) > zTol {
			t.Errorf("peak %d: individual redshift = %v, want 0.03", i, m.Redshift)
		}
		if m.ObservedWavelength != peaks[i] {
			t.Errorf("peak %d: observed = %v, want %v (peak order preserved)", i, m.ObservedWavelength, peaks[i])
		}
	}
}

// A single peak against the multi-line default catalog matches every
// line at a different redshift: ambiguous, never silently Hα at 0.03.
func TestIdentify_SinglePeakAmbiguous(t *testing.T) {
	cat := DefaultCatalog()
	cands, _ := Identify([]float64{675.9684}, cat)
	if len(cands) != len(cat.Lines) {
		t.Fatalf("got %d candidates, want one per catalog line (%d)", len(cands), len(cat.Lines))
	}
	seenHAlpha := false
	for _, c := range cands {
		if len(c.Matches) != 1 {
			t.Fatalf("single-peak candidate must have exactly 1 match: %+v", c)
		}
		if c.Matches[0].LineID == "Hα" {
			seenHAlpha = true
			if math.Abs(c.CommonRedshift-0.03) > zTol {
				t.Fatalf("Hα candidate redshift = %v, want 0.03", c.CommonRedshift)
			}
		}
	}
	if !seenHAlpha {
		t.Fatal("Hα must be among the candidates, but only as one of several")
	}
}

// Replacing 500.7139 with 520 nm breaks any common-redshift assignment.
func TestIdentify_NoConsistentAssignment(t *testing.T) {
	cands, _ := Identify([]float64{675.9684, 520, 515.721}, DefaultCatalog())
	if len(cands) != 0 {
		t.Fatalf("got %d candidates, want none: %+v", len(cands), cands)
	}
}

// Two identical peaks cannot share one line; with two identical-rest
// lines both permutations are distinct candidates.
func TestIdentify_DistinctLinesRequired(t *testing.T) {
	one := Catalog{Unit: "nm", Lines: []Line{{ID: "A", RestWavelength: 500}}}
	if cands, _ := Identify([]float64{515, 515}, one); len(cands) != 0 {
		t.Fatalf("one line cannot serve two peaks: got %d candidates", len(cands))
	}
	two := Catalog{Unit: "nm", Lines: []Line{
		{ID: "A", RestWavelength: 500},
		{ID: "B", RestWavelength: 500},
	}}
	cands, _ := Identify([]float64{515, 515}, two)
	if len(cands) != 2 {
		t.Fatalf("two identical lines must give the two permutations, got %d", len(cands))
	}
}

// The 200 km/s window (in the default linear relation) is the exact
// compatibility boundary between two peaks.
func TestIdentify_VelocityWindowBoundary(t *testing.T) {
	cat := Catalog{Unit: "nm", Lines: []Line{
		{ID: "A", RestWavelength: 500},
		{ID: "B", RestWavelength: 600},
	}}
	dz := VelocityWindowKmS / calc.SpeedOfLightKmS
	p1 := 500 * 1.03 // z = 0.03 on line A

	inside := 600 * (1.03 + 0.9*dz) // Δv ≈ 180 km/s: compatible
	cands, _ := Identify([]float64{p1, inside}, cat)
	if len(cands) != 1 {
		t.Fatalf("peaks within the window must match: got %d candidates", len(cands))
	}

	outside := 600 * (1.03 + 1.1*dz) // Δv ≈ 220 km/s: incompatible
	cands, _ = Identify([]float64{p1, outside}, cat)
	if len(cands) != 0 {
		t.Fatalf("peaks beyond the window must not match: got %d candidates", len(cands))
	}
}

// Two complete assignments at different common redshifts must both be
// returned as candidates.
func TestIdentify_TwoSolutionsBothListed(t *testing.T) {
	cat := Catalog{Unit: "nm", Lines: []Line{
		{ID: "A", RestWavelength: 500},
		{ID: "B", RestWavelength: 600},
		{ID: "C", RestWavelength: 1000},
		{ID: "D", RestWavelength: 1200},
	}}
	// Peaks fit A,B at z=0.03 and C,D at z=-0.485.
	cands, _ := Identify([]float64{515, 618}, cat)
	if len(cands) != 2 {
		t.Fatalf("got %d candidates, want 2: %+v", len(cands), cands)
	}
	if math.Abs(cands[0].CommonRedshift-(-0.485)) > zTol {
		t.Fatalf("first candidate z = %v, want -0.485", cands[0].CommonRedshift)
	}
	if math.Abs(cands[1].CommonRedshift-0.03) > zTol {
		t.Fatalf("second candidate z = %v, want 0.03", cands[1].CommonRedshift)
	}
}

// A consistent blueshift (z < 0) is a valid identification.
func TestIdentify_BlueshiftSolution(t *testing.T) {
	cat := Catalog{Unit: "nm", Lines: []Line{
		{ID: "A", RestWavelength: 500},
		{ID: "B", RestWavelength: 600},
	}}
	cands, _ := Identify([]float64{485, 582}, cat)
	if len(cands) != 1 {
		t.Fatalf("got %d candidates, want 1", len(cands))
	}
	if math.Abs(cands[0].CommonRedshift-(-0.03)) > zTol {
		t.Fatalf("common redshift = %v, want -0.03", cands[0].CommonRedshift)
	}
}

// Catalog validation: unit, non-empty, unique identities, positive
// rest wavelengths.
func TestCatalogValidate(t *testing.T) {
	cases := []struct {
		name string
		cat  Catalog
		kind ErrorKind
	}{
		{"no unit", Catalog{Lines: []Line{{ID: "A", RestWavelength: 500}}}, ErrMissingUnit},
		{"no lines", Catalog{Unit: "nm"}, ErrEmptyCatalog},
		{"missing id", Catalog{Unit: "nm", Lines: []Line{{RestWavelength: 500}}}, ErrMissingLineID},
		{"duplicate id", Catalog{Unit: "nm", Lines: []Line{
			{ID: "A", RestWavelength: 500}, {ID: "A", RestWavelength: 600}}}, ErrDuplicateLineID},
		{"zero wavelength", Catalog{Unit: "nm", Lines: []Line{{ID: "A", RestWavelength: 0}}}, ErrInvalidLineWavelength},
		{"negative wavelength", Catalog{Unit: "nm", Lines: []Line{{ID: "A", RestWavelength: -3}}}, ErrInvalidLineWavelength},
	}
	for _, c := range cases {
		err := c.cat.Validate()
		le, ok := err.(*Error)
		if !ok || le.Kind != c.kind {
			t.Errorf("%s: err = %v, want kind %v", c.name, err, c.kind)
		}
	}
	if err := DefaultCatalog().Validate(); err != nil {
		t.Fatalf("default catalog must be valid: %v", err)
	}
}

// The built-in catalog covers the three benchmark lines and enough
// other lines to make a lone peak ambiguous.
func TestDefaultCatalogContents(t *testing.T) {
	cat := DefaultCatalog()
	if cat.Unit != "nm" {
		t.Fatalf("default catalog unit = %q, want nm", cat.Unit)
	}
	byID := map[string]float64{}
	for _, ln := range cat.Lines {
		byID[ln.ID] = ln.RestWavelength
	}
	for id, want := range map[string]float64{"Hα": 656.28, "Hβ": 486.13, "[OIII]": 500.7} {
		if byID[id] != want {
			t.Errorf("default catalog %s = %v, want %v", id, byID[id], want)
		}
	}
	if len(cat.Lines) < 6 {
		t.Fatalf("default catalog has %d lines; it must hold enough lines for single-peak ambiguity", len(cat.Lines))
	}
}

// A snapshot is a deep copy: editing the source catalog afterwards
// leaves the snapshot untouched.
func TestSnapshotIsolation(t *testing.T) {
	cat := Catalog{ID: "x", Name: "x", Unit: "nm", Lines: []Line{
		{ID: "A", RestWavelength: 500},
		{ID: "B", RestWavelength: 600},
	}}
	snap := cat.Snapshot()
	cat.Lines[0].RestWavelength = 999
	if snap.Lines[0].RestWavelength != 500 {
		t.Fatalf("snapshot changed with the source catalog: %v", snap.Lines[0].RestWavelength)
	}
	back := snap.Catalog()
	back.Lines[1].RestWavelength = 111
	if snap.Lines[1].RestWavelength != 600 {
		t.Fatalf("rebuilding a catalog must not mutate the snapshot: %v", snap.Lines[1].RestWavelength)
	}
}
