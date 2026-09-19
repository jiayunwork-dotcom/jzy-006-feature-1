package store

import (
	"encoding/json"
	"math"
	"testing"

	"cosmocalc/internal/identify"
	"cosmocalc/internal/lines"
	"cosmocalc/internal/service"
)

// A registered custom catalogue must survive the JSONB encoding
// PostgreSQL applies (simulated here as a marshal/unmarshal round trip),
// including every line's stable identity and unit.
func TestCatalog_JSONRoundTripAcrossRestart(t *testing.T) {
	c := &lines.Catalog{
		ID: "mine", Name: "custom", WavelengthUnit: "nm", Source: lines.SourceCustom,
		Lines: []lines.Line{
			{ID: "HI_HA", Label: "H-alpha", RestWavelength: 656.28, WavelengthUnit: "nm"},
			{ID: "HI_HB", RestWavelength: 486.13, WavelengthUnit: "nm"},
		},
	}
	raw, err := json.Marshal(c.Lines)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored []lines.Line
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(restored) != 2 {
		t.Fatalf("restored %d lines, want 2", len(restored))
	}
	byID := map[string]lines.Line{}
	for _, l := range restored {
		byID[l.ID] = l
	}
	ha := byID["HI_HA"]
	if ha.RestWavelength != 656.28 || ha.WavelengthUnit != "nm" || ha.Label != "H-alpha" {
		t.Fatalf("H-alpha line not preserved across JSONB round trip: %+v", ha)
	}
}

// A persisted identification report must survive JSONB encoding intact:
// status, observed peaks, snapshot lines and candidates. A replay built
// purely from the decoded report must reproduce the original identities
// and common redshift — this is the "database restart, then replay" path.
func TestReport_JSONRoundTripAndReplay(t *testing.T) {
	reg := lines.NewRegistry(nil)
	svc := service.NewIdentifyService(reg, NewMemReports())
	h0 := 70.0
	resp, err := svc.Identify(service.IdentifyRequest{
		WavelengthUnit: "nm",
		CatalogID:      lines.DefaultCatalogID,
		HubbleConstant: &h0,
		Peaks: []service.IdentifyPeak{
			{ObservedWavelength: floatPtr(675.9684)},
			{ObservedWavelength: floatPtr(500.7139)},
			{ObservedWavelength: floatPtr(515.721)},
		},
	})
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	original, err := svc.GetReport(resp.ID)
	if err != nil {
		t.Fatalf("get report: %v", err)
	}

	// Simulate PostgreSQL JSONB persistence across a restart.
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	var restored service.IdentifyReport
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}

	if restored.Status != identify.StatusUnique {
		t.Fatalf("restored status = %v", restored.Status)
	}
	if restored.Snapshot.WavelengthUnit != "nm" || len(restored.Snapshot.Lines) < 10 {
		t.Fatalf("restored snapshot damaged: unit=%q lines=%d",
			restored.Snapshot.WavelengthUnit, len(restored.Snapshot.Lines))
	}
	if len(restored.ObservedWavelength) != 3 {
		t.Fatalf("restored observed peaks = %d", len(restored.ObservedWavelength))
	}

	// Replay purely from the restored payload.
	svc2 := service.NewIdentifyService(lines.NewRegistry(nil), NewMemReports())
	replayed := svc2.Replay(&restored)
	if replayed.Status != identify.StatusUnique {
		t.Fatalf("replay status = %v", replayed.Status)
	}
	if math.Abs(replayed.Candidate.MeanRedshift-0.03) > 1e-9 {
		t.Fatalf("replay z = %v, want 0.03", replayed.Candidate.MeanRedshift)
	}
	want := map[float64]string{675.9684: "HI_HA", 500.7139: "HI_HB", 515.721: "OIII_5007"}
	for _, m := range replayed.Candidate.Matches {
		if want[m.ObservedWavelength] != m.LineID {
			t.Fatalf("replay identity %v -> %v", m.ObservedWavelength, m.LineID)
		}
	}
	if replayed.Distance == nil || math.Abs(replayed.Distance.DistanceMpc-128.482) > 0.01 {
		t.Fatalf("replay distance = %+v, want ~128.48 Mpc", replayed.Distance)
	}
}

// The MemCatalogs/MemReports repositories return independent copies, so a
// caller mutating a fetched catalogue cannot corrupt stored state.
func TestMemRepositories_DeepCopies(t *testing.T) {
	cats := NewMemCatalogs()
	c := &lines.Catalog{ID: "c", WavelengthUnit: "nm", Source: lines.SourceCustom, Lines: []lines.Line{
		{ID: "A", RestWavelength: 100, WavelengthUnit: "nm"},
	}}
	if err := cats.SaveCatalog(c); err != nil {
		t.Fatalf("save: %v", err)
	}
	c.Lines[0].RestWavelength = 1 // mutate the caller's object
	got, err := cats.GetCatalog("c")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Lines[0].RestWavelength != 100 {
		t.Fatalf("stored catalogue aliased caller memory: %v", got.Lines[0].RestWavelength)
	}
	got.Lines[0].RestWavelength = 2 // mutate a fetched copy
	again, _ := cats.GetCatalog("c")
	if again.Lines[0].RestWavelength != 100 {
		t.Fatalf("stored catalogue aliased fetched memory: %v", again.Lines[0].RestWavelength)
	}

	reps := NewMemReports()
	rep := &service.IdentifyReport{Status: identify.StatusUnique, ObservedWavelength: []float64{1}}
	if err := reps.SaveReport(rep); err != nil {
		t.Fatalf("save report: %v", err)
	}
	rep.ObservedWavelength[0] = 9
	fetched, err := reps.GetReport(1)
	if err != nil {
		t.Fatalf("get report: %v", err)
	}
	if fetched.ObservedWavelength[0] != 1 {
		t.Fatalf("stored report aliased caller memory: %v", fetched.ObservedWavelength)
	}
}

func floatPtr(v float64) *float64 { return &v }
