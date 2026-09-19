package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ReportRecord is one persisted identification report. It carries the
// original request, the catalog snapshot the run matched against, and
// the outcome (identified / ambiguous / no_match), so the run can be
// reviewed and replayed later even after the source catalog changed.
//
// Reports are stored apart from the computation history (the
// `computations` table): they must never show up in history queries,
// with or without a type filter.
type ReportRecord struct {
	ID        int64           `json:"id"`
	Status    string          `json:"status"` // "identified", "ambiguous" or "no_match"
	Request   json.RawMessage `json:"request"`
	Snapshot  json.RawMessage `json:"snapshot"`
	Result    json.RawMessage `json:"result"`
	CreatedAt time.Time       `json:"created_at"`
}

// ---- in-memory implementation ----

// SaveReport appends a report, assigning ID and timestamp.
func (m *MemStore) SaveReport(_ context.Context, rep *ReportRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rep.ID = m.nextRepID
	m.nextRepID++
	rep.CreatedAt = time.Now().UTC()
	stored := *rep
	stored.Request = cloneRaw(rep.Request)
	stored.Snapshot = cloneRaw(rep.Snapshot)
	stored.Result = cloneRaw(rep.Result)
	m.reports = append(m.reports, stored)
	return nil
}

// GetReport returns an independent copy of one report.
func (m *MemStore) GetReport(_ context.Context, id int64) (*ReportRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.reports {
		if r.ID == id {
			out := r
			out.Request = cloneRaw(r.Request)
			out.Snapshot = cloneRaw(r.Snapshot)
			out.Result = cloneRaw(r.Result)
			return &out, nil
		}
	}
	return nil, ErrReportNotFound
}

// ListReports returns reports newest-first, honouring limit/offset.
func (m *MemStore) ListReports(_ context.Context, limit, offset int) ([]ReportRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	out := make([]ReportRecord, 0, limit)
	for i := len(m.reports) - 1; i >= 0; i-- {
		if offset > 0 {
			offset--
			continue
		}
		if len(out) >= limit {
			break
		}
		r := m.reports[i]
		r.Request = cloneRaw(r.Request)
		r.Snapshot = cloneRaw(r.Snapshot)
		r.Result = cloneRaw(r.Result)
		out = append(out, r)
	}
	return out, nil
}

// ---- PostgreSQL implementation ----

// SaveReport inserts a report and fills in its ID and timestamp.
func (p *PostgresStore) SaveReport(ctx context.Context, rep *ReportRecord) error {
	row := p.db.QueryRowContext(ctx,
		`INSERT INTO identification_reports (status, request, snapshot, result) VALUES ($1, $2, $3, $4)
		 RETURNING id, created_at`,
		rep.Status, rep.Request, rep.Snapshot, rep.Result)
	return row.Scan(&rep.ID, &rep.CreatedAt)
}

// GetReport fetches one report by ID.
func (p *PostgresStore) GetReport(ctx context.Context, id int64) (*ReportRecord, error) {
	var r ReportRecord
	row := p.db.QueryRowContext(ctx,
		`SELECT id, status, request, snapshot, result, created_at FROM identification_reports WHERE id = $1`, id)
	if err := row.Scan(&r.ID, &r.Status, &r.Request, &r.Snapshot, &r.Result, &r.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrReportNotFound
		}
		return nil, err
	}
	return &r, nil
}

// ListReports returns reports newest-first, honouring limit/offset.
func (p *PostgresStore) ListReports(ctx context.Context, limit, offset int) ([]ReportRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := p.db.QueryContext(ctx,
		`SELECT id, status, request, snapshot, result, created_at FROM identification_reports
		 ORDER BY id DESC LIMIT $1 OFFSET $2`, limit, max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReportRecord{}
	for rows.Next() {
		var r ReportRecord
		if err := rows.Scan(&r.ID, &r.Status, &r.Request, &r.Snapshot, &r.Result, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
