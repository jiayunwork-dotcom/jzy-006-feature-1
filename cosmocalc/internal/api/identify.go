// HTTP handlers for the spectral-line identification layer: rest-line
// catalog registration/retrieval, the identification entry point, and
// persisted identification reports with replay. Identification reports
// are stored apart from the computation history and never appear in
// the redshift/distance history endpoints.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"cosmocalc/internal/calc"
	"cosmocalc/internal/lines"
	"cosmocalc/internal/service"
	"cosmocalc/internal/store"
)

// registerIdentificationRoutes adds the identification-layer routes.
func (s *Server) registerIdentificationRoutes() {
	s.mux.HandleFunc("POST /api/v1/identify", s.handleIdentify)
	s.mux.HandleFunc("POST /api/v1/catalogs", s.handleCreateCatalog)
	s.mux.HandleFunc("GET /api/v1/catalogs", s.handleListCatalogs)
	s.mux.HandleFunc("GET /api/v1/catalogs/{id}", s.handleGetCatalog)
	s.mux.HandleFunc("PUT /api/v1/catalogs/{id}", s.handleUpdateCatalog)
	s.mux.HandleFunc("GET /api/v1/identifications", s.handleListReports)
	s.mux.HandleFunc("GET /api/v1/identifications/{id}", s.handleGetReport)
	s.mux.HandleFunc("POST /api/v1/identifications/{id}/replay", s.handleReplayReport)
}

// identifyAPIError maps an identification-layer error to its structured
// body and HTTP status.
func identifyAPIError(err error) (apiError, int) {
	if ie, ok := service.AsIdentifyError(err); ok {
		status := http.StatusBadRequest
		switch ie.Type {
		case service.ErrNoConsistentRedshift:
			status = http.StatusUnprocessableEntity
		case service.ErrCatalogNotFound, service.ErrReportNotFound:
			status = http.StatusNotFound
		}
		return apiError{Type: ie.Type, Message: ie.Message, Field: ie.Field}, status
	}
	var le *lines.Error
	if errors.As(err, &le) {
		return apiError{Type: string(le.Kind), Message: le.Message, Field: le.Field}, http.StatusBadRequest
	}
	return serviceAPIError(err)
}

// writeIdentifyErr writes a structured identification error, attaching
// the report ID when a report was persisted for the failed run.
func writeIdentifyErr(w http.ResponseWriter, err error, reportID int64) {
	ae, status := identifyAPIError(err)
	body := map[string]any{"error": ae}
	if reportID > 0 {
		body["report_id"] = reportID
	}
	writeJSON(w, status, body)
}

// parseCatalogRef accepts a catalog reference given as a JSON string
// ("default" or a decimal ID) or a JSON number.
func parseCatalogRef(raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var num json.Number
	if err := json.Unmarshal(raw, &num); err == nil {
		return num.String(), nil
	}
	return "", fmt.Errorf("catalog_id must be a string or a number")
}

// resolveCatalog picks the catalog for one identification run: an
// inline catalog if supplied, a registered one if catalog_id names one,
// otherwise the built-in default. Exactly one source is used — a custom
// table is never blended with the built-in lines.
func (s *Server) resolveCatalog(r *http.Request, in service.IdentifyInput) (lines.Catalog, error) {
	hasID := len(in.CatalogID) > 0 && string(in.CatalogID) != "null"
	if hasID && in.Catalog != nil {
		return lines.Catalog{}, &service.IdentifyError{Type: service.ErrInvalidInput, Field: "catalog",
			Message: "provide at most one of catalog_id and catalog"}
	}
	if in.Catalog != nil {
		cat := *in.Catalog
		if cat.ID == "" {
			cat.ID = lines.InlineCatalogID
		}
		return cat, nil
	}
	if !hasID {
		return lines.DefaultCatalog(), nil
	}
	ref, err := parseCatalogRef(in.CatalogID)
	if err != nil {
		return lines.Catalog{}, &service.IdentifyError{Type: service.ErrInvalidInput, Field: "catalog_id", Message: err.Error()}
	}
	if ref == "" || ref == lines.DefaultCatalogID {
		return lines.DefaultCatalog(), nil
	}
	id, perr := strconv.ParseInt(ref, 10, 64)
	if perr != nil {
		return lines.Catalog{}, &service.IdentifyError{Type: service.ErrInvalidInput, Field: "catalog_id",
			Message: fmt.Sprintf("catalog_id %q is neither %q nor a numeric catalog ID", ref, lines.DefaultCatalogID)}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rec, err := s.store.GetCatalog(ctx, id)
	if errors.Is(err, store.ErrCatalogNotFound) {
		return lines.Catalog{}, &service.IdentifyError{Type: service.ErrCatalogNotFound, Field: "catalog_id",
			Message: fmt.Sprintf("no registered catalog with id %d", id)}
	}
	if err != nil {
		return lines.Catalog{}, err
	}
	return catalogFromRecord(rec)
}

// catalogFromRecord converts a stored record back into a catalog.
func catalogFromRecord(rec *store.CatalogRecord) (lines.Catalog, error) {
	var ls []lines.Line
	if err := json.Unmarshal(rec.Lines, &ls); err != nil {
		return lines.Catalog{}, fmt.Errorf("stored catalog %d is corrupt: %w", rec.ID, err)
	}
	return lines.Catalog{
		ID:    strconv.FormatInt(rec.ID, 10),
		Name:  rec.Name,
		Unit:  rec.Unit,
		Lines: ls,
	}, nil
}

// handleIdentify runs one spectral-line identification. Every run that
// reaches matching — identified, ambiguous, or no-match — is persisted
// as a report carrying the catalog snapshot.
func (s *Server) handleIdentify(w http.ResponseWriter, r *http.Request) {
	var in service.IdentifyInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "", err.Error())
		return
	}
	if err := service.ValidateIdentifyInput(in); err != nil {
		writeIdentifyErr(w, err, 0)
		return
	}
	cat, err := s.resolveCatalog(r, in)
	if err != nil {
		writeIdentifyErr(w, err, 0)
		return
	}
	outcome, runErr := service.Identify(cat, in)
	if outcome == nil {
		// Rejected before matching (invalid catalog, unit mismatch):
		// no report is persisted.
		writeIdentifyErr(w, runErr, 0)
		return
	}
	reportID := s.persistIdentifyReport(r, in, outcome, runErr)
	if runErr != nil {
		writeIdentifyErr(w, runErr, reportID)
		return
	}
	writeJSON(w, http.StatusOK, identifyOutcomeBody(outcome, reportID))
}

// identifyOutcomeBody renders a completed identification outcome.
func identifyOutcomeBody(out *service.IdentifyOutcome, reportID int64) map[string]any {
	body := map[string]any{
		"status":          string(out.Status),
		"report_id":       reportID,
		"wavelength_unit": out.Snapshot.Unit,
		"catalog": map[string]any{
			"id":   out.Snapshot.CatalogID,
			"name": out.Snapshot.Name,
			"unit": out.Snapshot.Unit,
		},
	}
	switch out.Status {
	case service.StatusIdentified:
		body["common_redshift"] = out.Candidate.CommonRedshift
		body["shift_type"] = out.ShiftType
		body["velocity_kms"] = out.VelocityKmS
		body["matches"] = out.Candidate.Matches
		if out.Distance != nil {
			body["distance"] = out.Distance
		}
	case service.StatusAmbiguous:
		body["candidates"] = out.Candidates
		body["candidate_count"] = len(out.Candidates)
		if out.CandidatesTruncated {
			body["candidates_truncated"] = true
		}
		body["message"] = "multiple self-consistent identifications exist; all candidates are listed, none was picked"
	}
	return body
}

// persistIdentifyReport stores the run as a report and returns its ID.
// Reports are kept for every completed run — success, ambiguity, and
// no-match alike — and always carry the catalog snapshot.
func (s *Server) persistIdentifyReport(r *http.Request, in service.IdentifyInput, out *service.IdentifyOutcome, runErr error) int64 {
	result := map[string]any{}
	switch out.Status {
	case service.StatusIdentified:
		result["common_redshift"] = out.Candidate.CommonRedshift
		result["shift_type"] = out.ShiftType
		result["velocity_kms"] = out.VelocityKmS
		result["matches"] = out.Candidate.Matches
		if out.Distance != nil {
			result["distance"] = out.Distance
		}
		if runErr != nil {
			result["distance_error"] = runErr.Error()
		}
	case service.StatusAmbiguous:
		result["candidates"] = out.Candidates
		if out.CandidatesTruncated {
			result["candidates_truncated"] = true
		}
	default: // no_match
		if runErr != nil {
			result["error"] = runErr.Error()
		}
	}
	reqJSON, err1 := json.Marshal(in)
	snapJSON, err2 := json.Marshal(out.Snapshot)
	resJSON, err3 := json.Marshal(result)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0
	}
	rec := &store.ReportRecord{
		Status:   string(out.Status),
		Request:  reqJSON,
		Snapshot: snapJSON,
		Result:   resJSON,
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.store.SaveReport(ctx, rec); err != nil {
		return 0
	}
	return rec.ID
}

// ---- catalog registration endpoints ----

// handleCreateCatalog registers a caller-supplied rest-line catalog.
func (s *Server) handleCreateCatalog(w http.ResponseWriter, r *http.Request) {
	var cat lines.Catalog
	if err := decode(r, &cat); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "", err.Error())
		return
	}
	if cat.Name == "" {
		writeErr(w, http.StatusBadRequest, "missing_field", "name", "catalog name is required")
		return
	}
	if err := cat.Validate(); err != nil {
		writeIdentifyErr(w, err, 0)
		return
	}
	linesJSON, err := json.Marshal(cat.Lines)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "", err.Error())
		return
	}
	rec := &store.CatalogRecord{Name: cat.Name, Unit: cat.Unit, Lines: linesJSON}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.store.SaveCatalog(ctx, rec); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

// defaultCatalogView renders the built-in catalog for API responses.
func defaultCatalogView() map[string]any {
	cat := lines.DefaultCatalog()
	return map[string]any{
		"id":    cat.ID,
		"name":  cat.Name,
		"unit":  cat.Unit,
		"lines": cat.Lines,
	}
}

// handleListCatalogs lists the built-in default catalog followed by all
// registered catalogs.
func (s *Server) handleListCatalogs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	recs, err := s.store.ListCatalogs(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "", err.Error())
		return
	}
	catalogs := make([]any, 0, len(recs)+1)
	catalogs = append(catalogs, defaultCatalogView())
	for i := range recs {
		catalogs = append(catalogs, recs[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(catalogs), "catalogs": catalogs})
}

// handleGetCatalog returns the built-in default catalog ("default") or
// one registered catalog by numeric ID.
func (s *Server) handleGetCatalog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == lines.DefaultCatalogID {
		writeJSON(w, http.StatusOK, defaultCatalogView())
		return
	}
	num, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_input", "id", "catalog id must be \"default\" or a numeric ID")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rec, err := s.store.GetCatalog(ctx, num)
	if errors.Is(err, store.ErrCatalogNotFound) {
		writeErr(w, http.StatusNotFound, "catalog_not_found", "id", fmt.Sprintf("no registered catalog with id %d", num))
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleUpdateCatalog replaces a registered catalog's name/unit/lines.
// Runs that already started keep their snapshot; the next
// identification uses the updated table. The built-in default catalog
// is immutable.
func (s *Server) handleUpdateCatalog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == lines.DefaultCatalogID {
		writeErr(w, http.StatusBadRequest, "immutable_catalog", "id",
			"the built-in default catalog cannot be modified; register a custom catalog instead")
		return
	}
	num, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_input", "id", "catalog id must be a numeric ID")
		return
	}
	var cat lines.Catalog
	if err := decode(r, &cat); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "", err.Error())
		return
	}
	if cat.Name == "" {
		writeErr(w, http.StatusBadRequest, "missing_field", "name", "catalog name is required")
		return
	}
	if err := cat.Validate(); err != nil {
		writeIdentifyErr(w, err, 0)
		return
	}
	linesJSON, err := json.Marshal(cat.Lines)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "", err.Error())
		return
	}
	rec := &store.CatalogRecord{ID: num, Name: cat.Name, Unit: cat.Unit, Lines: linesJSON}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.store.UpdateCatalog(ctx, rec); errors.Is(err, store.ErrCatalogNotFound) {
		writeErr(w, http.StatusNotFound, "catalog_not_found", "id", fmt.Sprintf("no registered catalog with id %d", num))
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// ---- identification report endpoints ----

// handleListReports lists identification reports, newest first. Reports
// are a separate record type and never appear in computation history.
func (s *Server) handleListReports(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	reps, err := s.store.ListReports(ctx, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(reps), "reports": reps})
}

// getReport loads one report or writes the structured 404.
func (s *Server) getReport(w http.ResponseWriter, r *http.Request) *store.ReportRecord {
	id := r.PathValue("id")
	num, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_input", "id", "report id must be numeric")
		return nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rep, err := s.store.GetReport(ctx, num)
	if errors.Is(err, store.ErrReportNotFound) {
		writeErr(w, http.StatusNotFound, "report_not_found", "id", fmt.Sprintf("no identification report with id %d", num))
		return nil
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "", err.Error())
		return nil
	}
	return rep
}

// handleGetReport returns one persisted identification report including
// its request, catalog snapshot and outcome.
func (s *Server) handleGetReport(w http.ResponseWriter, r *http.Request) {
	rep := s.getReport(w, r)
	if rep == nil {
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// handleReplayReport re-runs the identification from the report's
// stored snapshot and request. Because the snapshot — not the live
// catalog — is matched, the replay reproduces the original identities
// and common redshift even if the catalog has since been edited.
func (s *Server) handleReplayReport(w http.ResponseWriter, r *http.Request) {
	rep := s.getReport(w, r)
	if rep == nil {
		return
	}
	var snapshot lines.Snapshot
	if err := json.Unmarshal(rep.Snapshot, &snapshot); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "", "stored snapshot is corrupt: "+err.Error())
		return
	}
	var in service.IdentifyInput
	if err := json.Unmarshal(rep.Request, &in); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "", "stored request is corrupt: "+err.Error())
		return
	}
	outcome, runErr := service.Identify(snapshot.Catalog(), in)
	if outcome == nil {
		writeErr(w, http.StatusInternalServerError, "internal", "", "replay failed: "+runErr.Error())
		return
	}
	body := identifyOutcomeBody(outcome, rep.ID)
	if outcome.Status == service.StatusNoMatch && runErr != nil {
		body["error"] = runErr.Error()
	}
	if runErr != nil && outcome.Status == service.StatusIdentified {
		if ce, ok := service.AsCalcError(runErr); ok && ce.Kind == calc.ErrBlueshiftDistance {
			body["distance_error"] = runErr.Error()
		}
	}
	body["consistent_with_report"] = replayConsistent(rep, outcome)
	writeJSON(w, http.StatusOK, body)
}

// storedReportResult mirrors the result payload persistIdentifyReport
// writes, for replay comparison.
type storedReportResult struct {
	CommonRedshift *float64          `json:"common_redshift"`
	Matches        []lines.Match     `json:"matches"`
	Candidates     []lines.Candidate `json:"candidates"`
	Error          string            `json:"error"`
}

// replayConsistent checks that a replayed outcome reproduces the
// report's stored outcome (same status, same identities, same common
// redshift).
func replayConsistent(rep *store.ReportRecord, out *service.IdentifyOutcome) bool {
	if string(out.Status) != rep.Status {
		return false
	}
	var stored storedReportResult
	if err := json.Unmarshal(rep.Result, &stored); err != nil {
		return false
	}
	switch out.Status {
	case service.StatusIdentified:
		if stored.CommonRedshift == nil || out.Candidate == nil {
			return false
		}
		if math.Abs(*stored.CommonRedshift-out.Candidate.CommonRedshift) > 1e-12 {
			return false
		}
		return matchesEqual(stored.Matches, out.Candidate.Matches)
	case service.StatusAmbiguous:
		if len(stored.Candidates) != len(out.Candidates) {
			return false
		}
		for i := range stored.Candidates {
			if math.Abs(stored.Candidates[i].CommonRedshift-out.Candidates[i].CommonRedshift) > 1e-12 {
				return false
			}
			if !matchesEqual(stored.Candidates[i].Matches, out.Candidates[i].Matches) {
				return false
			}
		}
		return true
	default: // no_match
		return stored.Error != ""
	}
}

// matchesEqual compares two match lists pairwise.
func matchesEqual(a, b []lines.Match) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].LineID != b[i].LineID ||
			a[i].RestWavelength != b[i].RestWavelength ||
			a[i].ObservedWavelength != b[i].ObservedWavelength ||
			math.Abs(a[i].Redshift-b[i].Redshift) > 1e-12 {
			return false
		}
	}
	return true
}
