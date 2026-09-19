package api

import (
	"math"
	"net/http"
	"sync"
	"testing"

	"cosmocalc/internal/lines"
)

// End-to-end golden benchmark: the three observed peaks are uniquely
// identified against the built-in table with the correct identities and
// z = 0.03.
func TestIdentifyEndpoint_GoldenGalaxy(t *testing.T) {
	s := newTestServer()
	rec, body := do(t, s, "POST", "/api/v1/identify", map[string]any{
		"wavelength_unit": "nm",
		"catalog_id":      lines.DefaultCatalogID,
		"peaks": []map[string]any{
			{"observed_wavelength": 675.9684},
			{"observed_wavelength": 500.7139},
			{"observed_wavelength": 515.721},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", rec.Code, body)
	}
	if body["status"] != "unique" {
		t.Fatalf("status = %v, want unique", body["status"])
	}
	cand := body["candidate"].(map[string]any)
	if math.Abs(f64(cand["redshift"])-0.03) > 1e-9 {
		t.Fatalf("common redshift = %v, want 0.03", cand["redshift"])
	}
	want := map[float64]string{675.9684: "HI_HA", 500.7139: "HI_HB", 515.721: "OIII_5007"}
	matches := cand["matches"].([]any)
	if len(matches) != 3 {
		t.Fatalf("matches = %d, want 3", len(matches))
	}
	for _, mm := range matches {
		m := mm.(map[string]any)
		obs := f64(m["observed_wavelength"])
		if m["line_id"] != want[obs] {
			t.Fatalf("peak %v identified as %v, want %v", obs, m["line_id"], want[obs])
		}
		if math.Abs(f64(m["redshift"])-0.03) > 1e-9 {
			t.Fatalf("per-line redshift = %v, want 0.03", m["redshift"])
		}
	}
	snap := body["snapshot"].(map[string]any)
	if snap["id"] != lines.DefaultCatalogID {
		t.Fatalf("snapshot id = %v", snap["id"])
	}
	if len(snap["lines"].([]any)) < 10 {
		t.Fatalf("snapshot must carry the used table, got %d lines", len(snap["lines"].([]any)))
	}
	if body["id"] == nil || f64(body["id"]) == 0 {
		t.Fatal("identification must return a retrievable report id")
	}
}

// The pairs produced by identification must reproduce z = 0.03 when fed
// back into the EXISTING redshift endpoint, and with H0 = 70 the distance
// must match the preset example.
func TestIdentifyPairs_FeedBackIntoExistingEndpoints(t *testing.T) {
	s := newTestServer()
	_, body := do(t, s, "POST", "/api/v1/identify", map[string]any{
		"wavelength_unit": "nm",
		"catalog_id":      lines.DefaultCatalogID,
		"peaks": []map[string]any{
			{"observed_wavelength": 675.9684},
			{"observed_wavelength": 500.7139},
			{"observed_wavelength": 515.721},
		},
	})
	cand := body["candidate"].(map[string]any)
	for _, mm := range cand["matches"].([]any) {
		m := mm.(map[string]any)
		_, red := do(t, s, "POST", "/api/v1/redshift", map[string]any{
			"rest_wavelength":     f64(m["rest_wavelength"]),
			"observed_wavelength": f64(m["observed_wavelength"]),
		})
		if math.Abs(f64(red["redshift"])-0.03) > 1e-9 {
			t.Fatalf("redshift endpoint returned %v for pair %v, want 0.03", red["redshift"], m["line_id"])
		}
		_, dist := do(t, s, "POST", "/api/v1/distance", map[string]any{
			"rest_wavelength":     f64(m["rest_wavelength"]),
			"observed_wavelength": f64(m["observed_wavelength"]),
			"hubble_constant":     70.0,
		})
		if math.Abs(f64(dist["distance_mpc"])-128.482) > 0.01 {
			t.Fatalf("distance = %v, want ~128.48 Mpc for %v", f64(dist["distance_mpc"]), m["line_id"])
		}
		if dist["linear_regime"] != true || dist["beyond_linear_threshold"] != false {
			t.Fatalf("linear-regime flags inconsistent with preset example: %v", dist)
		}
	}

	// The identification itself, when asked for H0=70, must report the same
	// distance as the existing distance computation.
	rec, withDist := do(t, s, "POST", "/api/v1/identify", map[string]any{
		"wavelength_unit": "nm",
		"catalog_id":      lines.DefaultCatalogID,
		"hubble_constant": 70.0,
		"peaks": []map[string]any{
			{"observed_wavelength": 675.9684},
			{"observed_wavelength": 500.7139},
			{"observed_wavelength": 515.721},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", rec.Code, withDist)
	}
	d := withDist["distance"].(map[string]any)
	if math.Abs(f64(d["distance_mpc"])-128.482) > 0.01 {
		t.Fatalf("identify distance = %v, want ~128.48 Mpc", f64(d["distance_mpc"]))
	}
}

// One peak facing the multi-line table must come back 200 ambiguous with
// multiple candidates, never as a single H-alpha @ z=0.03 answer.
func TestIdentifyEndpoint_SinglePeakAmbiguous(t *testing.T) {
	s := newTestServer()
	rec, body := do(t, s, "POST", "/api/v1/identify", map[string]any{
		"wavelength_unit": "nm",
		"catalog_id":      lines.DefaultCatalogID,
		"peaks":           []map[string]any{{"observed_wavelength": 675.9684}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %v", rec.Code, body)
	}
	if body["status"] != "ambiguous" {
		t.Fatalf("status = %v, want ambiguous", body["status"])
	}
	cands := body["candidates"].([]any)
	if len(cands) < 2 {
		t.Fatalf("candidates = %d, want several", len(cands))
	}
	// H-alpha @ z=0.03 must be listed, not hidden.
	found := false
	for _, cc := range cands {
		c := cc.(map[string]any)
		m := c["matches"].([]any)[0].(map[string]any)
		if m["line_id"] == "HI_HA" && math.Abs(f64(c["redshift"])-0.03) < 1e-9 {
			found = true
		}
	}
	if !found {
		t.Fatalf("H-alpha @ z=0.03 missing from candidates: %v", cands)
	}
}

// Changing 500.7139 to 520 yields a structured 422 no-match (with a
// report id), never a made-up redshift.
func TestIdentifyEndpoint_NoMatchStructuredRejection(t *testing.T) {
	s := newTestServer()
	rec, body := do(t, s, "POST", "/api/v1/identify", map[string]any{
		"wavelength_unit": "nm",
		"catalog_id":      lines.DefaultCatalogID,
		"peaks": []map[string]any{
			{"observed_wavelength": 675.9684},
			{"observed_wavelength": 500.7139},
			{"observed_wavelength": 520.0},
		},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %v", rec.Code, body)
	}
	errObj := body["error"].(map[string]any)
	if errObj["type"] != "no_consistent_match" {
		t.Fatalf("error type = %v, want no_consistent_match", errObj["type"])
	}
	if body["report_id"] == nil || f64(body["report_id"]) == 0 {
		t.Fatal("no-match response must carry the persisted report id")
	}
	// The no-match report is retrievable by id.
	rid := int64(f64(body["report_id"]))
	_, get := do(t, s, "GET", reportPath(rid), nil)
	report := get["report"].(map[string]any)
	if report["status"] != "no_match" {
		t.Fatalf("stored report status = %v, want no_match", report["status"])
	}
}

// Reports never enter the redshift/distance computation history, neither
// type-filtered nor unfiltered; the old history content/count is unchanged
// by identifications.
func TestIdentifyReports_NotInComputationHistory(t *testing.T) {
	s := newTestServer()

	// Pre-existing computation history from before identification.
	do(t, s, "POST", "/api/v1/redshift", map[string]any{"redshift": 0.02})
	do(t, s, "POST", "/api/v1/distance", map[string]any{"redshift": 0.02, "hubble_constant": 70})

	// Several identifications of every outcome.
	do(t, s, "POST", "/api/v1/identify", map[string]any{
		"wavelength_unit": "nm", "catalog_id": lines.DefaultCatalogID,
		"peaks": []map[string]any{{"observed_wavelength": 675.9684}, {"observed_wavelength": 500.7139}, {"observed_wavelength": 515.721}},
	})
	do(t, s, "POST", "/api/v1/identify", map[string]any{
		"wavelength_unit": "nm", "catalog_id": lines.DefaultCatalogID,
		"peaks": []map[string]any{{"observed_wavelength": 675.9684}},
	})
	do(t, s, "POST", "/api/v1/identify", map[string]any{
		"wavelength_unit": "nm", "catalog_id": lines.DefaultCatalogID,
		"peaks": []map[string]any{{"observed_wavelength": 675.9684}, {"observed_wavelength": 500.7139}, {"observed_wavelength": 520.0}},
	})

	_, all := do(t, s, "GET", "/api/v1/history?limit=100", nil)
	if got := f64(all["count"]); got != 2 {
		t.Fatalf("unfiltered computation history = %v, want 2 (identifications must not leak in)", got)
	}
	for _, rr := range all["records"].([]any) {
		typ := rr.(map[string]any)["type"].(string)
		if typ != "redshift" && typ != "distance" {
			t.Fatalf("unexpected history type %q", typ)
		}
	}
	_, red := do(t, s, "GET", "/api/v1/history?type=redshift", nil)
	if f64(red["count"]) != 1 {
		t.Fatalf("redshift history = %v, want 1", red["count"])
	}
	_, dist := do(t, s, "GET", "/api/v1/history?type=distance", nil)
	if f64(dist["count"]) != 1 {
		t.Fatalf("distance history = %v, want 1", dist["count"])
	}

	// The reports live on their own surface.
	_, reps := do(t, s, "GET", "/api/v1/identifications?limit=100", nil)
	if got := f64(reps["count"]); got != 3 {
		t.Fatalf("identification reports = %v, want 3", got)
	}
}

// Snapshot replay: identify against a registered custom table, then edit
// the table, then replay the report — identities and common redshift must
// reproduce the original.
func TestIdentifyEndpoint_ReplayAfterTableEdit(t *testing.T) {
	s := newTestServer()
	rec, catBody := do(t, s, "POST", "/api/v1/catalogs", map[string]any{
		"id":              "bench",
		"name":            "benchmark table",
		"wavelength_unit": "nm",
		"lines": []map[string]any{
			{"id": "HI_HA", "rest_wavelength": 656.28},
			{"id": "HI_HB", "rest_wavelength": 486.13},
			{"id": "OIII_5007", "rest_wavelength": 500.7},
			{"id": "NII_6584", "rest_wavelength": 658.34},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register catalog status = %d body = %v", rec.Code, catBody)
	}

	_, idBody := do(t, s, "POST", "/api/v1/identify", map[string]any{
		"wavelength_unit": "nm",
		"catalog_id":      "bench",
		"peaks": []map[string]any{
			{"observed_wavelength": 675.9684},
			{"observed_wavelength": 500.7139},
			{"observed_wavelength": 515.721},
		},
	})
	if idBody["status"] != "unique" {
		t.Fatalf("status = %v, want unique", idBody["status"])
	}
	rid := int64(f64(idBody["id"]))
	if math.Abs(f64(idBody["candidate"].(map[string]any)["redshift"])-0.03) > 1e-9 {
		t.Fatal("original identification must be z=0.03")
	}

	// Edit the table after the identification.
	if r, b := do(t, s, "PATCH", "/api/v1/catalogs/bench/lines/HI_HA", map[string]any{
		"rest_wavelength": 900.0,
	}); r.Code != http.StatusOK {
		t.Fatalf("patch status = %d body = %v", r.Code, b)
	}

	// Replay the stored report: same identities, same z, snapshot frozen at
	// 656.28.
	_, replay := do(t, s, "POST", reportPath(rid)+"/replay", nil)
	if replay["status"] != "unique" {
		t.Fatalf("replay status = %v, want unique", replay["status"])
	}
	if replay["replayed"] != true {
		t.Fatal("replay must be marked as a replay")
	}
	rc := replay["candidate"].(map[string]any)
	if math.Abs(f64(rc["redshift"])-0.03) > 1e-9 {
		t.Fatalf("replay redshift = %v, want 0.03 despite table edit", rc["redshift"])
	}
	ids := map[float64]string{675.9684: "HI_HA", 500.7139: "HI_HB", 515.721: "OIII_5007"}
	for _, mm := range rc["matches"].([]any) {
		m := mm.(map[string]any)
		if ids[f64(m["observed_wavelength"])] != m["line_id"] {
			t.Fatalf("replay identity drift: %v -> %v", f64(m["observed_wavelength"]), m["line_id"])
		}
	}
	snapLines := replay["snapshot"].(map[string]any)["lines"].([]any)
	for _, ll := range snapLines {
		l := ll.(map[string]any)
		if l["id"] == "HI_HA" && math.Abs(f64(l["rest_wavelength"])-656.28) > 1e-9 {
			t.Fatalf("snapshot wavelength drifted to %v", l["rest_wavelength"])
		}
	}

	// A FRESH identification must use the edited table and not find z=0.03.
	r2, fresh := do(t, s, "POST", "/api/v1/identify", map[string]any{
		"wavelength_unit": "nm",
		"catalog_id":      "bench",
		"peaks": []map[string]any{
			{"observed_wavelength": 675.9684},
			{"observed_wavelength": 500.7139},
			{"observed_wavelength": 515.721},
		},
	})
	if r2.Code == http.StatusOK && fresh["status"] == "unique" &&
		math.Abs(f64(fresh["candidate"].(map[string]any)["redshift"])-0.03) < 1e-6 {
		t.Fatal("new identification must not reproduce z=0.03 from the edited table")
	}
}

// Pre-matching input rejections use types distinct from missing_field and
// non_positive_wavelength.
func TestIdentifyEndpoint_ValidationErrors(t *testing.T) {
	s := newTestServer()
	cases := []struct {
		name       string
		wantStatus int
		body       map[string]any
		wantType   string
	}{
		{"empty peaks", http.StatusBadRequest, map[string]any{
			"wavelength_unit": "nm", "catalog_id": lines.DefaultCatalogID,
			"peaks": []map[string]any{},
		}, "empty_peaks"},
		{"missing unit", http.StatusBadRequest, map[string]any{
			"catalog_id": lines.DefaultCatalogID,
			"peaks":      []map[string]any{{"observed_wavelength": 600}},
		}, "missing_wavelength_unit"},
		{"non-positive peak", http.StatusBadRequest, map[string]any{
			"wavelength_unit": "nm", "catalog_id": lines.DefaultCatalogID,
			"peaks": []map[string]any{{"observed_wavelength": -5}},
		}, "non_positive_observed_wavelength"},
		{"unit mismatch", http.StatusBadRequest, map[string]any{
			"wavelength_unit": "Angstrom", "catalog_id": lines.DefaultCatalogID,
			"peaks": []map[string]any{{"observed_wavelength": 6759.684}},
		}, "wavelength_unit_mismatch"},
		{"empty inline catalog", http.StatusBadRequest, map[string]any{
			"wavelength_unit": "nm",
			"catalog":         map[string]any{"wavelength_unit": "nm", "lines": []map[string]any{}},
			"peaks":           []map[string]any{{"observed_wavelength": 600}},
		}, "empty_catalog"},
		{"unknown catalog", http.StatusNotFound, map[string]any{
			"wavelength_unit": "nm", "catalog_id": "ghost",
			"peaks": []map[string]any{{"observed_wavelength": 600}},
		}, "catalog_not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, body := do(t, s, "POST", "/api/v1/identify", tc.body)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %v", rec.Code, tc.wantStatus, body)
			}
			errObj := body["error"].(map[string]any)
			if errObj["type"] != tc.wantType {
				t.Fatalf("error type = %v, want %v", errObj["type"], tc.wantType)
			}
		})
	}
}

// The built-in table is retrievable and read-only; registering under its
// id is refused.
func TestCatalogEndpoints_BuiltinAndReadOnly(t *testing.T) {
	s := newTestServer()
	_, body := do(t, s, "GET", "/api/v1/catalogs/"+lines.DefaultCatalogID, nil)
	cat := body["catalog"].(map[string]any)
	if f64(cat["line_count"]) < 10 {
		t.Fatalf("builtin line count = %v", cat["line_count"])
	}
	if cat["wavelength_unit"] != "nm" {
		t.Fatalf("builtin unit = %v", cat["wavelength_unit"])
	}
	rec, patch := do(t, s, "PATCH", "/api/v1/catalogs/"+lines.DefaultCatalogID+"/lines/HI_HA",
		map[string]any{"rest_wavelength": 700})
	if rec.Code == http.StatusOK {
		t.Fatalf("builtin table must be read-only, body = %v", patch)
	}
	if patch["error"].(map[string]any)["type"] != "builtin_catalog_read_only" {
		t.Fatalf("error = %v, want builtin_catalog_read_only", patch["error"])
	}
}

// Two simultaneous identifications using different one-shot inline tables
// must never exchange lines, even under concurrency.
func TestIdentifyEndpoint_ConcurrentInlineTablesIsolated(t *testing.T) {
	s := newTestServer()
	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	peaks := []map[string]any{
		{"observed_wavelength": 675.9684},
		{"observed_wavelength": 500.7139},
		{"observed_wavelength": 515.721},
	}
	tableA := map[string]any{
		"wavelength_unit": "nm",
		"lines": []map[string]any{
			{"id": "A_HA", "rest_wavelength": 656.28},
			{"id": "A_HB", "rest_wavelength": 486.13},
			{"id": "A_O3", "rest_wavelength": 500.7},
		},
	}
	tableB := map[string]any{
		"wavelength_unit": "nm",
		"lines": []map[string]any{
			{"id": "B_X", "rest_wavelength": 600},
			{"id": "B_Y", "rest_wavelength": 670},
		},
	}
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			rec, body, err := doReq(s, "POST", "/api/v1/identify", map[string]any{
				"wavelength_unit": "nm", "catalog": tableA, "peaks": peaks,
			})
			if err != nil || rec.Code != http.StatusOK {
				errs <- errOrStatus(err, rec.Code)
				return
			}
			if body["status"] != "unique" {
				errs <- errOrStatus(nil, rec.Code)
				return
			}
			for _, mm := range body["candidate"].(map[string]any)["matches"].([]any) {
				id := mm.(map[string]any)["line_id"].(string)
				if len(id) < 2 || id[:2] != "A_" {
					errs <- errOrStatus(nil, http.StatusInternalServerError)
				}
			}
		}()
		go func() {
			defer wg.Done()
			rec, body, err := doReq(s, "POST", "/api/v1/identify", map[string]any{
				"wavelength_unit": "nm", "catalog": tableB,
				"peaks": []map[string]any{{"observed_wavelength": 675.9684}},
			})
			if err != nil || rec.Code != http.StatusOK || body["status"] != "ambiguous" {
				errs <- errOrStatus(err, rec.Code)
				return
			}
			for _, cc := range body["candidates"].([]any) {
				id := cc.(map[string]any)["matches"].([]any)[0].(map[string]any)["line_id"].(string)
				if len(id) < 2 || id[:2] != "B_" {
					errs <- errOrStatus(nil, http.StatusInternalServerError)
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// None of the inline identifications registered the one-shot tables.
	_, list := do(t, s, "GET", "/api/v1/catalogs", nil)
	for _, cc := range list["catalogs"].([]any) {
		id := cc.(map[string]any)["id"].(string)
		if id != lines.DefaultCatalogID {
			t.Fatalf("inline table leaked into registered catalogs: %v", id)
		}
	}
}

// A blueshift identification may request a distance, which is refused with
// the existing blueshift_distance type and no distance field.
func TestIdentifyEndpoint_BlueshiftDistanceRefused(t *testing.T) {
	s := newTestServer()
	rec, body := do(t, s, "POST", "/api/v1/identify", map[string]any{
		"wavelength_unit": "nm",
		"hubble_constant": 70.0,
		"catalog": map[string]any{
			"wavelength_unit": "nm",
			"lines":           []map[string]any{{"id": "A", "rest_wavelength": 600}},
		},
		"peaks": []map[string]any{{"observed_wavelength": 500}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (identification succeeds); body = %v", rec.Code, body)
	}
	cand := body["candidate"].(map[string]any)
	if cand["shift_type"] != "blueshift" {
		t.Fatalf("shift_type = %v, want blueshift", cand["shift_type"])
	}
	de := body["distance_error"].(map[string]any)
	if de["type"] != "blueshift_distance" {
		t.Fatalf("distance_error = %v, want blueshift_distance", de)
	}
	if _, leaked := body["distance"]; leaked {
		t.Fatal("blueshift identification must not return a distance")
	}
}

func reportPath(id int64) string {
	return "/api/v1/identifications/" + itoa(id)
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func errOrStatus(err error, code int) error {
	if err != nil {
		return err
	}
	return errReport(code)
}

type errReport int

func (e errReport) Error() string { return "unexpected HTTP status " + itoa(int64(e)) }
