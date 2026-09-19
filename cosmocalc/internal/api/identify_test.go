package api

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sync"
	"testing"
)

// ---- helpers ----

func identify(t *testing.T, s *Server, body map[string]any) (int, map[string]any) {
	t.Helper()
	rec, parsed := do(t, s, "POST", "/api/v1/identify", body)
	return rec.Code, parsed
}

func mustIdentifyOK(t *testing.T, s *Server, body map[string]any) map[string]any {
	t.Helper()
	code, parsed := identify(t, s, body)
	if code != http.StatusOK {
		t.Fatalf("identify status = %d, body = %v", code, parsed)
	}
	return parsed
}

func errType(t *testing.T, body map[string]any) string {
	t.Helper()
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing structured error: %v", body)
	}
	return errObj["type"].(string)
}

func matchesOf(t *testing.T, body map[string]any) []any {
	t.Helper()
	m, ok := body["matches"].([]any)
	if !ok {
		t.Fatalf("missing matches: %v", body)
	}
	return m
}

// ---- benchmark acceptance ----

// The three benchmark peaks identify uniquely as Hα/Hβ/[OIII] at
// z = 0.03; feeding the pairs back into the existing redshift endpoint
// reproduces 0.03, and with H0 = 70 the distance matches the existing
// distance endpoint and the preset example.
func TestIdentifyEndpoint_BenchmarkTrio(t *testing.T) {
	s := newTestServer()
	body := mustIdentifyOK(t, s, map[string]any{
		"peaks":           []float64{675.9684, 500.7139, 515.721},
		"wavelength_unit": "nm",
	})
	if body["status"] != "identified" {
		t.Fatalf("status = %v, want identified: %v", body["status"], body)
	}
	if math.Abs(f64(body["common_redshift"])-0.03) > 1e-9 {
		t.Fatalf("common redshift = %v, want 0.03", body["common_redshift"])
	}
	if body["report_id"] == nil || f64(body["report_id"]) <= 0 {
		t.Fatalf("identified run must return its report id: %v", body)
	}
	wantIDs := []string{"Hα", "Hβ", "[OIII]"}
	wantRest := []float64{656.28, 486.13, 500.7}
	matches := matchesOf(t, body)
	if len(matches) != 3 {
		t.Fatalf("got %d matches, want 3", len(matches))
	}
	for i, m := range matches {
		mm := m.(map[string]any)
		if mm["line_id"] != wantIDs[i] {
			t.Fatalf("match %d: identity = %v, want %v", i, mm["line_id"], wantIDs[i])
		}
		if math.Abs(f64(mm["rest_wavelength"])-wantRest[i]) > 1e-12 {
			t.Fatalf("match %d: rest = %v, want %v", i, mm["rest_wavelength"], wantRest[i])
		}
		if math.Abs(f64(mm["redshift"])-0.03) > 1e-9 {
			t.Fatalf("match %d: redshift = %v, want 0.03", i, mm["redshift"])
		}
		// The identified pair must reproduce z = 0.03 through the
		// existing (pre-paired) redshift endpoint.
		_, rz := do(t, s, "POST", "/api/v1/redshift", map[string]any{
			"rest_wavelength":     f64(mm["rest_wavelength"]),
			"observed_wavelength": f64(mm["observed_wavelength"]),
		})
		if math.Abs(f64(rz["redshift"])-0.03) > 1e-9 {
			t.Fatalf("existing redshift endpoint on identified pair %d: z = %v, want 0.03", i, rz["redshift"])
		}
	}

	// With H0 = 70 the distance must match the existing distance
	// endpoint and the preset example, including the linear verdict.
	withDist := mustIdentifyOK(t, s, map[string]any{
		"peaks":           []float64{675.9684, 500.7139, 515.721},
		"wavelength_unit": "nm",
		"hubble_constant": 70,
	})
	dist, ok := withDist["distance"].(map[string]any)
	if !ok {
		t.Fatalf("distance must be included when hubble_constant is given: %v", withDist)
	}
	if math.Abs(f64(dist["distance_mpc"])-128.482) > 0.01 {
		t.Fatalf("distance = %v Mpc, want ~128.48", dist["distance_mpc"])
	}
	_, existing := do(t, s, "POST", "/api/v1/distance", map[string]any{"redshift": 0.03, "hubble_constant": 70})
	if math.Abs(f64(dist["distance_mpc"])-f64(existing["distance_mpc"])) > 1e-9 {
		t.Fatalf("identify distance %v != existing distance %v", dist["distance_mpc"], existing["distance_mpc"])
	}
	if dist["linear_regime"] != existing["linear_regime"] ||
		dist["beyond_linear_threshold"] != existing["beyond_linear_threshold"] {
		t.Fatalf("linear-regime verdict differs: identify=%v existing=%v", dist, existing)
	}
	_, demo := do(t, s, "GET", "/api/v1/demo", nil)
	demoRes := demo["result"].(map[string]any)
	if math.Abs(f64(dist["distance_mpc"])-f64(demoRes["distance_mpc"])) > 1e-9 {
		t.Fatalf("identify distance %v != preset example %v", dist["distance_mpc"], demoRes["distance_mpc"])
	}
}

// A single peak against the default catalog must be reported as
// ambiguous — never silently identified as Hα at z = 0.03.
func TestIdentifyEndpoint_SinglePeakAmbiguous(t *testing.T) {
	s := newTestServer()
	body := mustIdentifyOK(t, s, map[string]any{
		"peaks":           []float64{675.9684},
		"wavelength_unit": "nm",
	})
	if body["status"] != "ambiguous" {
		t.Fatalf("status = %v, want ambiguous: %v", body["status"], body)
	}
	if _, leaked := body["common_redshift"]; leaked {
		t.Fatalf("an ambiguous run must not present a single common redshift: %v", body)
	}
	cands := body["candidates"].([]any)
	if len(cands) < 3 {
		t.Fatalf("got %d candidates, want several", len(cands))
	}
	// Hα at 0.03 is one candidate among several — not "the" answer.
	seenHAlpha := false
	for _, c := range cands {
		cm := c.(map[string]any)
		for _, m := range cm["matches"].([]any) {
			if m.(map[string]any)["line_id"] == "Hα" {
				seenHAlpha = true
			}
		}
	}
	if !seenHAlpha {
		t.Fatal("Hα must appear among the candidates")
	}
	if body["report_id"] == nil {
		t.Fatal("ambiguous runs are persisted too")
	}
}

// Replacing 500.7139 with 520 nm leaves no consistent assignment:
// structured rejection, no fudged redshift.
func TestIdentifyEndpoint_NoMatch(t *testing.T) {
	s := newTestServer()
	code, body := identify(t, s, map[string]any{
		"peaks":           []float64{675.9684, 520, 515.721},
		"wavelength_unit": "nm",
	})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %v", code, body)
	}
	if got := errType(t, body); got != "no_consistent_redshift" {
		t.Fatalf("error type = %v, want no_consistent_redshift", got)
	}
	if _, leaked := body["common_redshift"]; leaked {
		t.Fatalf("a failed run must not return a redshift: %v", body)
	}
	if body["report_id"] == nil {
		t.Fatal("failed runs are persisted as reports too")
	}
}

// Pre-matching rejections: structured, and distinct from the existing
// missing_field / non_positive_wavelength types.
func TestIdentifyEndpoint_ValidationErrors(t *testing.T) {
	s := newTestServer()
	cases := []struct {
		name     string
		body     map[string]any
		wantType string
	}{
		{"empty peaks", map[string]any{"peaks": []float64{}, "wavelength_unit": "nm"}, "empty_peaks"},
		{"no peaks field", map[string]any{"wavelength_unit": "nm"}, "empty_peaks"},
		{"missing unit", map[string]any{"peaks": []float64{500}}, "missing_unit"},
		{"negative peak", map[string]any{"peaks": []float64{-1}, "wavelength_unit": "nm"}, "invalid_peak_wavelength"},
		{"zero peak", map[string]any{"peaks": []float64{0}, "wavelength_unit": "nm"}, "invalid_peak_wavelength"},
		{"unit mismatch", map[string]any{"peaks": []float64{500}, "wavelength_unit": "angstrom"}, "unit_mismatch"},
		{"empty inline catalog", map[string]any{
			"peaks": []float64{500}, "wavelength_unit": "nm",
			"catalog": map[string]any{"unit": "nm", "lines": []any{}},
		}, "empty_catalog"},
		{"catalog not found", map[string]any{
			"peaks": []float64{500}, "wavelength_unit": "nm", "catalog_id": "777",
		}, "catalog_not_found"},
	}
	for _, c := range cases {
		code, body := identify(t, s, c.body)
		if code == http.StatusOK {
			t.Errorf("%s: must be rejected, got 200: %v", c.name, body)
			continue
		}
		got := errType(t, body)
		if got != c.wantType {
			t.Errorf("%s: error type = %v, want %v (body=%v)", c.name, got, c.wantType, body)
		}
		if got == "missing_field" || got == "non_positive_wavelength" {
			t.Errorf("%s: type %q collides with existing computation errors", c.name, got)
		}
	}
}

// An inline custom catalog is used exclusively for that run.
func TestIdentifyEndpoint_InlineCatalogExclusive(t *testing.T) {
	s := newTestServer()
	body := mustIdentifyOK(t, s, map[string]any{
		"peaks":           []float64{656.28}, // would be z=0 on built-in Hα
		"wavelength_unit": "nm",
		"catalog": map[string]any{
			"name": "toy", "unit": "nm",
			"lines": []map[string]any{
				{"id": "X", "rest_wavelength": 1000},
				{"id": "Y", "rest_wavelength": 2000},
			},
		},
	})
	if body["status"] != "ambiguous" {
		t.Fatalf("status = %v, want ambiguous (2 inline candidates)", body["status"])
	}
	cands := body["candidates"].([]any)
	if len(cands) != 2 {
		t.Fatalf("got %d candidates, want exactly 2 (built-in lines must not leak in)", len(cands))
	}
	for _, c := range cands {
		m := c.(map[string]any)["matches"].([]any)[0].(map[string]any)
		if id := m["line_id"]; id != "X" && id != "Y" {
			t.Fatalf("candidate used line %v — built-in catalog leaked in", id)
		}
	}
}

// Catalog registration, retrieval, listing and update over HTTP.
func TestCatalogEndpoints_CRUD(t *testing.T) {
	s := newTestServer()

	// The built-in default catalog is always available.
	rec, def := do(t, s, "GET", "/api/v1/catalogs/default", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("default catalog: status %d", rec.Code)
	}
	defLines := def["lines"].([]any)
	byID := map[string]float64{}
	for _, l := range defLines {
		lm := l.(map[string]any)
		byID[lm["id"].(string)] = f64(lm["rest_wavelength"])
	}
	for id, want := range map[string]float64{"Hα": 656.28, "Hβ": 486.13, "[OIII]": 500.7} {
		if byID[id] != want {
			t.Fatalf("default catalog %s = %v, want %v", id, byID[id], want)
		}
	}
	if def["unit"] != "nm" {
		t.Fatalf("default catalog unit = %v, want nm", def["unit"])
	}

	// Register a custom catalog.
	rec, created := do(t, s, "POST", "/api/v1/catalogs", map[string]any{
		"name": "lab-lines", "unit": "nm",
		"lines": []map[string]any{
			{"id": "A", "rest_wavelength": 500},
			{"id": "B", "rest_wavelength": 600},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create catalog: status %d, body %v", rec.Code, created)
	}
	catID := f64(created["id"])

	// Retrieve it.
	_, got := do(t, s, "GET", fmt.Sprintf("/api/v1/catalogs/%v", catID), nil)
	if got["name"] != "lab-lines" || len(got["lines"].([]any)) != 2 {
		t.Fatalf("retrieved catalog wrong: %v", got)
	}

	// It appears in the list alongside the default.
	_, list := do(t, s, "GET", "/api/v1/catalogs", nil)
	if f64(list["count"]) < 2 {
		t.Fatalf("list must include default + registered: %v", list)
	}

	// Identify against it: peaks 515/618 are z=0.03 on A/B.
	body := mustIdentifyOK(t, s, map[string]any{
		"peaks":           []float64{515, 618},
		"wavelength_unit": "nm",
		"catalog_id":      catID,
	})
	if body["status"] != "identified" {
		t.Fatalf("status = %v, want identified: %v", body["status"], body)
	}
	for i, m := range matchesOf(t, body) {
		want := []string{"A", "B"}[i]
		if m.(map[string]any)["line_id"] != want {
			t.Fatalf("match %d: identity = %v, want %v", i, m, want)
		}
	}

	// Update the catalog: A moves 500 → 505. The next identification
	// uses the updated table and no longer matches.
	rec, _ = do(t, s, "PUT", fmt.Sprintf("/api/v1/catalogs/%v", catID), map[string]any{
		"name": "lab-lines", "unit": "nm",
		"lines": []map[string]any{
			{"id": "A", "rest_wavelength": 505},
			{"id": "B", "rest_wavelength": 600},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update catalog: status %d", rec.Code)
	}
	code, nm := identify(t, s, map[string]any{
		"peaks":           []float64{515, 618},
		"wavelength_unit": "nm",
		"catalog_id":      catID,
	})
	if code != http.StatusUnprocessableEntity || errType(t, nm) != "no_consistent_redshift" {
		t.Fatalf("after update the run must fail against the new table: %d %v", code, nm)
	}

	// The default catalog is immutable.
	rec, imm := do(t, s, "PUT", "/api/v1/catalogs/default", map[string]any{
		"name": "x", "unit": "nm", "lines": []map[string]any{{"id": "A", "rest_wavelength": 1}},
	})
	if rec.Code != http.StatusBadRequest || errType(t, imm) != "immutable_catalog" {
		t.Fatalf("default catalog must be immutable: %d %v", rec.Code, imm)
	}
}

// Reports are retrievable by ID, carry the snapshot, and replay
// reproduces the original result even after the catalog was edited.
func TestIdentifyReport_RetrieveAndReplay(t *testing.T) {
	s := newTestServer()

	_, created := do(t, s, "POST", "/api/v1/catalogs", map[string]any{
		"name": "replay-lines", "unit": "nm",
		"lines": []map[string]any{
			{"id": "A", "rest_wavelength": 500},
			{"id": "B", "rest_wavelength": 600},
		},
	})
	catID := f64(created["id"])

	body := mustIdentifyOK(t, s, map[string]any{
		"peaks":           []float64{515, 618},
		"wavelength_unit": "nm",
		"catalog_id":      catID,
	})
	reportID := f64(body["report_id"])

	// The report is retrievable and carries the snapshot actually used.
	rec, rep := do(t, s, "GET", fmt.Sprintf("/api/v1/identifications/%v", reportID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get report: status %d", rec.Code)
	}
	if rep["status"] != "identified" {
		t.Fatalf("report status = %v, want identified", rep["status"])
	}
	snap := rep["snapshot"].(map[string]any)
	snapLines := snap["lines"].([]any)
	if len(snapLines) != 2 {
		t.Fatalf("snapshot must carry the lines used: %v", snap)
	}
	first := snapLines[0].(map[string]any)
	if first["id"] != "A" || f64(first["rest_wavelength"]) != 500 {
		t.Fatalf("snapshot line A = %v, want 500", first)
	}
	result := rep["result"].(map[string]any)
	if math.Abs(f64(result["common_redshift"])-0.03) > 1e-9 {
		t.Fatalf("report redshift = %v, want 0.03", result["common_redshift"])
	}

	// The catalog is edited afterwards: A moves 500 → 510.
	do(t, s, "PUT", fmt.Sprintf("/api/v1/catalogs/%v", catID), map[string]any{
		"name": "replay-lines", "unit": "nm",
		"lines": []map[string]any{
			{"id": "A", "rest_wavelength": 510},
			{"id": "B", "rest_wavelength": 600},
		},
	})

	// Replay still reproduces the original identities and redshift.
	rec, replay := do(t, s, "POST", fmt.Sprintf("/api/v1/identifications/%v/replay", reportID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("replay: status %d", rec.Code)
	}
	if replay["status"] != "identified" {
		t.Fatalf("replay status = %v, want identified: %v", replay["status"], replay)
	}
	if math.Abs(f64(replay["common_redshift"])-0.03) > 1e-9 {
		t.Fatalf("replay redshift = %v, want 0.03", replay["common_redshift"])
	}
	for i, m := range matchesOf(t, replay) {
		mm := m.(map[string]any)
		wantID := []string{"A", "B"}[i]
		wantRest := []float64{500, 600}[i]
		if mm["line_id"] != wantID || math.Abs(f64(mm["rest_wavelength"])-wantRest) > 1e-12 {
			t.Fatalf("replay match %d = %v, want %v@%v", i, mm, wantID, wantRest)
		}
	}
	if replay["consistent_with_report"] != true {
		t.Fatalf("replay must be consistent with the stored report: %v", replay)
	}

	// Reports list contains the run.
	_, list := do(t, s, "GET", "/api/v1/identifications", nil)
	if f64(list["count"]) != 1 {
		t.Fatalf("reports list count = %v, want 1", list["count"])
	}

	// Unknown report → structured 404.
	rec, nf := do(t, s, "GET", "/api/v1/identifications/999", nil)
	if rec.Code != http.StatusNotFound || errType(t, nf) != "report_not_found" {
		t.Fatalf("missing report: %d %v", rec.Code, nf)
	}
}

// Replay works for every outcome: ambiguous reports replay the same
// candidate sets, no-match reports replay the same rejection.
func TestIdentifyReport_ReplayAmbiguousAndNoMatch(t *testing.T) {
	s := newTestServer()

	amb := mustIdentifyOK(t, s, map[string]any{
		"peaks": []float64{675.9684}, "wavelength_unit": "nm",
	})
	ambID := f64(amb["report_id"])
	rec, ambReplay := do(t, s, "POST", fmt.Sprintf("/api/v1/identifications/%v/replay", ambID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("replay ambiguous: status %d", rec.Code)
	}
	if ambReplay["status"] != "ambiguous" || ambReplay["consistent_with_report"] != true {
		t.Fatalf("ambiguous replay must reproduce the candidates: %v", ambReplay["status"])
	}
	if f64(ambReplay["candidate_count"]) != f64(amb["candidate_count"]) {
		t.Fatalf("replay candidate count %v != original %v", ambReplay["candidate_count"], amb["candidate_count"])
	}

	code, nm := identify(t, s, map[string]any{
		"peaks": []float64{675.9684, 520, 515.721}, "wavelength_unit": "nm",
	})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("no-match setup: status %d", code)
	}
	nmID := f64(nm["report_id"])
	rec, nmReplay := do(t, s, "POST", fmt.Sprintf("/api/v1/identifications/%v/replay", nmID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("replay no-match: status %d", rec.Code)
	}
	if nmReplay["status"] != "no_match" || nmReplay["consistent_with_report"] != true {
		t.Fatalf("no-match replay must reproduce the rejection: %v", nmReplay)
	}
}

// Identification reports never appear in the computation history —
// neither under the redshift/distance type filters nor unfiltered —
// and pre-existing history is untouched.
func TestIdentifyReports_StayOutOfHistory(t *testing.T) {
	s := newTestServer()

	// Pre-existing computation history.
	do(t, s, "POST", "/api/v1/redshift", map[string]any{"redshift": 0.02})
	do(t, s, "POST", "/api/v1/distance", map[string]any{"redshift": 0.02, "hubble_constant": 70})

	countHistory := func(query string) float64 {
		_, body := do(t, s, "GET", "/api/v1/history?"+query, nil)
		return f64(body["count"])
	}
	if countHistory("type=redshift") != 1 || countHistory("type=distance") != 1 || countHistory("") != 2 {
		t.Fatal("seed history wrong")
	}
	_, before := do(t, s, "GET", "/api/v1/history?limit=100", nil)
	beforeJSON, _ := json.Marshal(before["records"])

	// Run identifications of every outcome.
	mustIdentifyOK(t, s, map[string]any{ // identified
		"peaks": []float64{675.9684, 500.7139, 515.721}, "wavelength_unit": "nm",
	})
	mustIdentifyOK(t, s, map[string]any{ // ambiguous
		"peaks": []float64{675.9684}, "wavelength_unit": "nm",
	})
	identify(t, s, map[string]any{ // no_match
		"peaks": []float64{675.9684, 520, 515.721}, "wavelength_unit": "nm",
	})

	// History counts and contents are exactly as before.
	if got := countHistory("type=redshift"); got != 1 {
		t.Fatalf("redshift history = %v, want 1 (reports must not leak in)", got)
	}
	if got := countHistory("type=distance"); got != 1 {
		t.Fatalf("distance history = %v, want 1 (reports must not leak in)", got)
	}
	if got := countHistory(""); got != 2 {
		t.Fatalf("unfiltered history = %v, want 2 (reports must not leak in)", got)
	}
	_, after := do(t, s, "GET", "/api/v1/history?limit=100", nil)
	afterJSON, _ := json.Marshal(after["records"])
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("pre-existing history records changed")
	}

	// The three runs did land as reports.
	_, list := do(t, s, "GET", "/api/v1/identifications?limit=100", nil)
	if f64(list["count"]) != 3 {
		t.Fatalf("reports count = %v, want 3", list["count"])
	}
}

// Two catalogs identified concurrently: each run uses its own lines.
func TestIdentifyEndpoint_ConcurrentCatalogsNoCrossTalk(t *testing.T) {
	s := newTestServer()
	mkCatalog := func(name string, rest1, rest2 float64) float64 {
		_, created := do(t, s, "POST", "/api/v1/catalogs", map[string]any{
			"name": name, "unit": "nm",
			"lines": []map[string]any{
				{"id": name + "-1", "rest_wavelength": rest1},
				{"id": name + "-2", "rest_wavelength": rest2},
			},
		})
		return f64(created["id"])
	}
	catA := mkCatalog("catA", 500, 600)
	catB := mkCatalog("catB", 1000, 1200)

	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			useA := i%2 == 0
			catID, peaks, prefix := catB, []float64{1030, 1236}, "catB-"
			if useA {
				catID, peaks, prefix = catA, []float64{515, 618}, "catA-"
			}
			rec, body, err := doReq(s, "POST", "/api/v1/identify", map[string]any{
				"peaks": peaks, "wavelength_unit": "nm", "catalog_id": catID,
			})
			if err != nil {
				errs <- err
				return
			}
			if rec.Code != http.StatusOK || body["status"] != "identified" {
				errs <- fmt.Errorf("run %d: status %d body %v", i, rec.Code, body)
				return
			}
			for _, m := range body["matches"].([]any) {
				id := m.(map[string]any)["line_id"].(string)
				if len(id) < len(prefix) || id[:len(prefix)] != prefix {
					errs <- fmt.Errorf("run %d: line %q does not belong to %s", i, id, prefix)
					return
				}
			}
			if math.Abs(body["common_redshift"].(float64)-0.03) > 1e-9 {
				errs <- fmt.Errorf("run %d: z = %v, want 0.03", i, body["common_redshift"])
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// A blueshift identification is marked as such; asking for a distance
// on it is rejected with the standard blueshift rule.
func TestIdentifyEndpoint_Blueshift(t *testing.T) {
	s := newTestServer()
	cat := map[string]any{
		"unit": "nm",
		"lines": []map[string]any{
			{"id": "A", "rest_wavelength": 500},
			{"id": "B", "rest_wavelength": 600},
			{"id": "C", "rest_wavelength": 700},
		},
	}
	peaks := []float64{485, 582, 679} // z = -0.03 on A/B/C

	body := mustIdentifyOK(t, s, map[string]any{
		"peaks": peaks, "wavelength_unit": "nm", "catalog": cat,
	})
	if body["status"] != "identified" || body["shift_type"] != "blueshift" {
		t.Fatalf("blueshift must be identified and marked: %v", body)
	}
	if math.Abs(f64(body["common_redshift"])+0.03) > 1e-9 {
		t.Fatalf("common redshift = %v, want -0.03", body["common_redshift"])
	}

	code, rejected := identify(t, s, map[string]any{
		"peaks": peaks, "wavelength_unit": "nm", "catalog": cat, "hubble_constant": 70,
	})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %v", code, rejected)
	}
	if got := errType(t, rejected); got != "blueshift_distance" {
		t.Fatalf("error type = %v, want blueshift_distance", got)
	}
	if _, leaked := rejected["distance_mpc"]; leaked {
		t.Fatalf("blueshift must not be answered with a distance: %v", rejected)
	}
	if rejected["report_id"] == nil {
		t.Fatal("the blueshift-distance rejection is still a persisted report")
	}
}
