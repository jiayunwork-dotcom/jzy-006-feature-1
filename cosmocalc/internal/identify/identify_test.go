package identify

import (
	"math"
	"testing"

	"cosmocalc/internal/calc"
	"cosmocalc/internal/lines"
)

// The benchmark galaxy at z = 0.03: observed peaks 675.9684, 500.7139,
// 515.721 nm must be uniquely identified as H-alpha, H-beta and [OIII]
// against the shipped table.
func TestGoldenGalaxy_Z003_UniqueIdentification(t *testing.T) {
	snap := lines.BuiltinCatalog()
	peaks := []float64{675.9684, 500.7139, 515.721}

	res := Run(snap, peaks)
	if res.Status != StatusUnique {
		t.Fatalf("status = %v, want unique; candidates = %+v", res.Status, res.Candidates)
	}
	if len(res.Candidates) != 1 {
		t.Fatalf("got %d candidate clusters, want 1", len(res.Candidates))
	}
	cand := res.Candidates[0]
	if math.Abs(cand.MeanRedshift-0.03) > 1e-9 {
		t.Fatalf("common redshift = %v, want 0.03", cand.MeanRedshift)
	}
	if cand.MaxSpreadKmS > MaxVelocitySpreadKmS {
		t.Fatalf("velocity spread %v exceeds %v", cand.MaxSpreadKmS, MaxVelocitySpreadKmS)
	}
	byObs := map[float64]PeakMatch{}
	for _, m := range cand.Matches {
		byObs[m.ObservedWavelength] = m
	}
	want := map[float64]struct {
		line string
		rest float64
	}{
		675.9684: {"HI_HA", 656.28},
		500.7139: {"HI_HB", 486.13},
		515.721:  {"OIII_5007", 500.7},
	}
	if len(byObs) != 3 {
		t.Fatalf("got %d peak matches, want 3", len(byObs))
	}
	for obs, w := range want {
		m, ok := byObs[obs]
		if !ok {
			t.Fatalf("no match for observed peak %v", obs)
		}
		if m.LineID != w.line {
			t.Fatalf("peak %v identified as %v, want %v", obs, m.LineID, w.line)
		}
		if math.Abs(m.RestWavelength-w.rest) > 1e-9 {
			t.Fatalf("peak %v rest wavelength = %v, want %v", obs, m.RestWavelength, w.rest)
		}
		if math.Abs(m.Redshift-0.03) > 1e-9 {
			t.Fatalf("peak %v per-line redshift = %v, want 0.03", obs, m.Redshift)
		}
	}

	// Identities must be pairwise distinct (a bijection).
	seen := map[string]bool{}
	for _, m := range cand.Matches {
		if seen[m.LineID] {
			t.Fatalf("line %v assigned to two peaks", m.LineID)
		}
		seen[m.LineID] = true
	}
}

// One peak facing the multi-line table must NOT silently come back as
// H-alpha at z = 0.03: it lands at several different redshifts and must be
// reported as ambiguous.
func TestSinglePeak_MultiLineTable_IsAmbiguous(t *testing.T) {
	snap := lines.BuiltinCatalog()
	res := Run(snap, []float64{675.9684})
	if res.Status != StatusAmbiguous {
		t.Fatalf("status = %v, want ambiguous", res.Status)
	}
	if len(res.Candidates) < 2 {
		t.Fatalf("got %d candidate, want several distinct redshifts", len(res.Candidates))
	}
	// Candidate common redshifts must be separated by more than the
	// window in velocity terms.
	for i := 1; i < len(res.Candidates); i++ {
		dv := math.Abs(res.Candidates[i].MeanVelocityKmS - res.Candidates[i-1].MeanVelocityKmS)
		if dv <= MaxVelocitySpreadKmS {
			t.Fatalf("ambiguous candidates %d/%d not separated by more than %v km/s: dv=%v",
				i-1, i, MaxVelocitySpreadKmS, dv)
		}
	}
	// H-alpha at z = 0.03 must be among the listed candidates.
	found := false
	for _, c := range res.Candidates {
		if math.Abs(c.MeanRedshift-0.03) < 1e-9 && c.Matches[0].LineID == "HI_HA" {
			found = true
		}
	}
	if !found {
		t.Fatalf("H-alpha @ z=0.03 missing from ambiguity candidates: %+v", res.Candidates)
	}
}

// Replacing the [OIII] peak with 520 nm makes the three peaks impossible
// to place in one window: a structured no-match, never a made-up redshift.
func TestModifiedPeak_NoMatch(t *testing.T) {
	snap := lines.BuiltinCatalog()
	res := Run(snap, []float64{675.9684, 500.7139, 520})
	if res.Status != StatusNoMatch {
		t.Fatalf("status = %v, want no_match; candidates = %+v", res.Status, res.Candidates)
	}
	if len(res.Candidates) != 0 {
		t.Fatalf("no_match must not carry candidates, got %d", len(res.Candidates))
	}
}

// Every per-line velocity in a returned unique/ambiguous candidate must
// actually lie within the fixed window of one another.
func TestCandidates_SatisfyVelocityWindow(t *testing.T) {
	snap := lines.BuiltinCatalog()
	for _, peaks := range [][]float64{
		{675.9684, 500.7139, 515.721},
		{675.9684},
		{700, 490},
		{486.13, 500.7},
	} {
		res := Run(snap, peaks)
		for ci, c := range res.Candidates {
			minV, maxV := math.Inf(1), math.Inf(-1)
			usedLines := map[string]bool{}
			for _, m := range c.Matches {
				wantV := calc.SpeedOfLightKmS * m.Redshift
				if math.Abs(wantV-m.VelocityKmS) > 1e-6 {
					t.Fatalf("peaks %v candidate %d: velocity inconsistent with redshift", peaks, ci)
				}
				minV = math.Min(minV, m.VelocityKmS)
				maxV = math.Max(maxV, m.VelocityKmS)
				if usedLines[m.LineID] {
					t.Fatalf("peaks %v candidate %d: line %v reused", peaks, ci, m.LineID)
				}
				usedLines[m.LineID] = true
			}
			if maxV-minV > MaxVelocitySpreadKmS+1e-6 {
				t.Fatalf("peaks %v candidate %d spread %v > %v", peaks, ci, maxV-minV, MaxVelocitySpreadKmS)
			}
			if len(c.Matches) != len(peaks) {
				t.Fatalf("peaks %v candidate %d matches %d, want %d", peaks, ci, len(c.Matches), len(peaks))
			}
		}
	}
}

// Two genuinely different solutions with the same number of peaks must
// both be returned when their common redshifts lie outside the window.
func TestAmbiguous_TwoDistinctSolutionsListed(t *testing.T) {
	// Tiny table: one line at 100, another at 200. Both peaks are built so
	// that peak0=101 (z=0.01 against line100) and peak1=202 (z=0.01 against
	// line200): the unique solution is identity matching... add a third
	// peak to force cross-matching ambiguity instead.
	cat := &lines.Catalog{ID: "tiny", WavelengthUnit: "nm", Source: lines.SourceInline, Lines: []lines.Line{
		{ID: "A", RestWavelength: 100, WavelengthUnit: "nm"},
		{ID: "B", RestWavelength: 200, WavelengthUnit: "nm"},
	}}
	// Single peak 101: A gives z=0.01 (v=2998), B gives z=-0.495
	// (v=-148397). Two clusters far apart -> ambiguous, both listed.
	res := Run(cat, []float64{101})
	if res.Status != StatusAmbiguous || len(res.Candidates) != 2 {
		t.Fatalf("got status=%v candidates=%d, want ambiguous with 2 candidates", res.Status, len(res.Candidates))
	}
	ids := map[string]bool{}
	for _, c := range res.Candidates {
		ids[c.Matches[0].LineID] = true
	}
	if !ids["A"] || !ids["B"] {
		t.Fatalf("both identities must be listed, got %v", ids)
	}
}

// A blueshift identification is allowed and labelled blueshift; the
// distance refusal lives in the service layer.
func TestBlueshift_IdentifiedAndLabelled(t *testing.T) {
	cat := &lines.Catalog{ID: "tiny", WavelengthUnit: "nm", Source: lines.SourceInline, Lines: []lines.Line{
		{ID: "A", RestWavelength: 600, WavelengthUnit: "nm"},
	}}
	res := Run(cat, []float64{500})
	if res.Status != StatusUnique {
		t.Fatalf("status = %v, want unique", res.Status)
	}
	cand := res.Candidates[0]
	if cand.MeanRedshift >= 0 || cand.ShiftType != "blueshift" {
		t.Fatalf("want negative blueshift, got z=%v type=%v", cand.MeanRedshift, cand.ShiftType)
	}
}

// The matcher must use the snapshot it is handed: mutating the catalogue
// after Run started cannot change the result. Run is synchronous, so the
// deterministic version of the guarantee is "the engine never reads
// anything but the snapshot" — feed it a deep copy and then mutate the
// original table.
func TestRun_UsesSnapshotOnly(t *testing.T) {
	original := lines.BuiltinCatalog()
	snapshot := original.Clone()

	before := Run(snapshot, []float64{675.9684, 500.7139, 515.721})

	// Corrupt the original table wholesale.
	for i := range original.Lines {
		original.Lines[i].RestWavelength = 1
	}
	after := Run(snapshot, []float64{675.9684, 500.7139, 515.721})

	if before.Status != StatusUnique || after.Status != StatusUnique {
		t.Fatalf("both runs must be unique, got %v/%v", before.Status, after.Status)
	}
	if math.Abs(after.Candidates[0].MeanRedshift-before.Candidates[0].MeanRedshift) > 1e-12 {
		t.Fatalf("snapshot result changed after table mutation: %v vs %v",
			after.Candidates[0].MeanRedshift, before.Candidates[0].MeanRedshift)
	}
	if after.Candidates[0].Matches[0].RestWavelength != before.Candidates[0].Matches[0].RestWavelength {
		t.Fatalf("snapshot rest wavelengths must be frozen")
	}
}

// The bijection requirement matters: with two peaks that both only fit the
// same single line there must be no match.
func TestTwoPeaksOneFittingLine_NoMatch(t *testing.T) {
	cat := &lines.Catalog{ID: "tiny", WavelengthUnit: "nm", Source: lines.SourceInline, Lines: []lines.Line{
		{ID: "A", RestWavelength: 100, WavelengthUnit: "nm"},
		{ID: "B", RestWavelength: 400, WavelengthUnit: "nm"},
	}}
	// Both peaks ~101: only A fits z~0.01 for either peak; B implies
	// z~-0.75 for both. They cannot share a window using distinct lines.
	res := Run(cat, []float64{101, 101.05})
	if res.Status != StatusNoMatch {
		t.Fatalf("status = %v, want no_match (one line cannot serve two peaks)", res.Status)
	}
}

// Regression test for the pairwise nature of the window: an
// anchor-centred [v_a-W, v_a+W] test would wrongly accept a bijection
// whose two matched velocities are nearly 2W apart (each within W of the
// anchor but NOT within W of each other). Peaks are designed so that
// pairing peak0 with line A (v=0) leaves peak1 only pairable with line B
// (v≈+1.9W); the spread ≈ 1.9W > W, so it must be rejected even though
// both are inside the anchor window.
func TestPairwiseSpread_NotJustAnchorWindow(t *testing.T) {
	const c = calc.SpeedOfLightKmS
	w := MaxVelocitySpreadKmS
	// peak0=1000; line A rest 1000 -> v=0. line C rest chosen so peak0->C
	// would sit near +1.9W too; peak1 must have no alternative within W of
	// the anchor except B (the far one).
	restA := 1000.0
	obs0 := 1000.0
	vB := 1.9 * w // B implies this velocity for peak1
	obs1 := 500.0
	restB := obs1 / (1 + vB/c) // > 500
	// C for peak0 at +1.9W would tie; give peak0 another nearby line D with
	// v = +0.5W so the anchor also has a *valid* +W neighbour, while B is
	// the unique option for peak1 at +1.9W (peak1 at 0 only matches B at
	// 1.9W — add line E so peak1 at 0 matches E at exactly 0? then a valid
	// solution exists (peak0->A 0, peak1->E 0), which is fine: the +1.9W
	// pairing must still never appear as a candidate).
	restE := 500.0 // peak1 -> E at v=0
	cat := &lines.Catalog{ID: "spread", WavelengthUnit: "nm", Source: lines.SourceInline, Lines: []lines.Line{
		{ID: "A", RestWavelength: restA, WavelengthUnit: "nm"},
		{ID: "B", RestWavelength: restB, WavelengthUnit: "nm"},
		{ID: "E", RestWavelength: restE, WavelengthUnit: "nm"},
	}}
	res := Run(cat, []float64{obs0, obs1})
	if res.Status == StatusNoMatch {
		t.Fatal("the (A,E) pairing at v=0 is a valid solution; expected unique")
	}
	for _, cand := range res.Candidates {
		if cand.MaxSpreadKmS > MaxVelocitySpreadKmS+tolVelocity {
			t.Fatalf("candidate spread %v exceeds %v: %+v", cand.MaxSpreadKmS, MaxVelocitySpreadKmS, cand.Matches)
		}
		ids := map[string]bool{}
		for _, m := range cand.Matches {
			if m.VelocityKmS < -w-tolVelocity || m.VelocityKmS > w+tolVelocity {
				t.Fatalf("accepted far-out pairing v=%v: %+v", m.VelocityKmS, cand.Matches)
			}
			ids[m.LineID] = true
		}
		if ids["B"] {
			t.Fatalf("the v≈+1.9W line B must never be paired inside a solution: %+v", cand.Matches)
		}
	}
}

const tolVelocity = 1e-6
