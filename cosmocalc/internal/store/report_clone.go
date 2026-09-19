package store

import (
	"errors"

	"cosmocalc/internal/identify"
	"cosmocalc/internal/lines"
	"cosmocalc/internal/service"
)

// ErrReportNotFound signals a missing identification report.
var ErrReportNotFound = errors.New("identification report not found")

// cloneReport deep-copies a report so in-memory callers can never mutate a
// stored report (this is what makes "identify, then edit the table, then
// replay" work without aliasing).
func cloneReport(r *service.IdentifyReport) *service.IdentifyReport {
	cp := *r
	cp.ObservedWavelength = append([]float64(nil), r.ObservedWavelength...)
	if r.HubbleConstant != nil {
		v := *r.HubbleConstant
		cp.HubbleConstant = &v
	}
	snap := r.Snapshot
	snap.Lines = append([]service.SnapshotLine(nil), r.Snapshot.Lines...)
	cp.Snapshot = snap
	cp.Candidates = append([]identify.Candidate(nil), r.Candidates...)
	for i := range cp.Candidates {
		cp.Candidates[i].Matches = append([]identify.PeakMatch(nil), r.Candidates[i].Matches...)
	}
	if r.DistanceResult != nil {
		d := *r.DistanceResult
		cp.DistanceResult = &d
	}
	if r.DistanceError != nil {
		e := *r.DistanceError
		cp.DistanceError = &e
	}
	_ = lines.DefaultCatalogID
	return &cp
}
