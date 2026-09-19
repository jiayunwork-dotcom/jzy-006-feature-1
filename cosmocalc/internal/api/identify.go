package api

import (
	"errors"
	"net/http"
	"strconv"

	"cosmocalc/internal/identify"
	"cosmocalc/internal/service"
	"cosmocalc/internal/store"
)

// handleIdentify runs line identification against a snapshot of either a
// registered table or the one-shot inline table. A report is persisted
// for every matcher outcome (unique / ambiguous / no-match); validation
// failures are rejected before matching and are not reports.
func (s *Server) handleIdentify(w http.ResponseWriter, r *http.Request) {
	var req service.IdentifyRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "", err.Error())
		return
	}
	resp, err := s.identify.Identify(req)
	if err != nil {
		writeMappedErr(w, err)
		return
	}
	// Unique and ambiguous are normal 200 responses; a structural failure
	// to make ALL peaks share one velocity window is 422 carrying the
	// report id and snapshot, never a made-up redshift.
	if resp.Status == identify.StatusNoMatch {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": apiError{
				Type:    "no_consistent_match",
				Message: "no bijection of peaks to table lines makes all per-line velocities agree within the window",
			},
			"report_id": resp.ID,
			"report":    resp,
		})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleGetReport returns the raw stored report by id.
func (s *Server) handleGetReport(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_report_id", "id", "report id must be an integer")
		return
	}
	rep, err := s.identify.GetReport(id)
	if err != nil {
		if errors.Is(err, store.ErrReportNotFound) {
			writeErr(w, http.StatusNotFound, "report_not_found", "id", "no identification report with id "+strconv.FormatInt(id, 10))
			return
		}
		writeMappedErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"report": rep})
}

// handleListReports pages identification reports newest-first. This is a
// separate surface from /api/v1/history: computation history never lists
// these reports, with or without a type filter.
func (s *Server) handleListReports(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	reps, err := s.identify.ListReports(limit, offset)
	if err != nil {
		writeMappedErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(reps), "reports": reps})
}

// handleReplay re-runs an identification from its stored snapshot. The
// referenced table may have changed since; identities and the common
// redshift still reproduce the original report.
func (s *Server) handleReplay(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_report_id", "id", "report id must be an integer")
		return
	}
	rep, err := s.identify.GetReport(id)
	if err != nil {
		if errors.Is(err, store.ErrReportNotFound) {
			writeErr(w, http.StatusNotFound, "report_not_found", "id", "no identification report with id "+strconv.FormatInt(id, 10))
			return
		}
		writeMappedErr(w, err)
		return
	}
	resp := s.identify.Replay(rep)
	status := http.StatusOK
	if resp.Status == identify.StatusNoMatch {
		status = http.StatusUnprocessableEntity
	}
	writeJSON(w, status, resp)
}
