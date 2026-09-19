package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"cosmocalc/internal/service"
)

// SaveReport persists an identification report (with its snapshot) and
// fills in its id and timestamp.
func (p *PostgresStore) SaveReport(ctx context.Context, rep *service.IdentifyReport) error {
	payload, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	row := p.db.QueryRowContext(ctx,
		`INSERT INTO identification_reports (status, payload) VALUES ($1, $2)
		 RETURNING id, created_at`,
		string(rep.Status), payload)
	var (
		id        int64
		createdAt time.Time
	)
	if err := row.Scan(&id, &createdAt); err != nil {
		return err
	}
	rep.ID = id
	rep.CreatedAt = createdAt
	return nil
}

// GetReport loads one identification report, restoring id/created_at from
// the table columns.
func (p *PostgresStore) GetReport(ctx context.Context, id int64) (*service.IdentifyReport, error) {
	var (
		createdAt time.Time
		payload   []byte
	)
	err := p.db.QueryRowContext(ctx,
		`SELECT id, created_at, payload FROM identification_reports WHERE id = $1`, id).
		Scan(&id, &createdAt, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrReportNotFound
	}
	if err != nil {
		return nil, err
	}
	return decodeReport(id, createdAt, payload)
}

// ListReports returns reports newest-first with paging, restoring
// id/created_at from the table columns.
func (p *PostgresStore) ListReports(ctx context.Context, limit, offset int) ([]*service.IdentifyReport, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := p.db.QueryContext(ctx,
		fmt.Sprintf(`SELECT id, created_at, payload FROM identification_reports ORDER BY id DESC LIMIT %d OFFSET %d`, limit, offset))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*service.IdentifyReport{}
	for rows.Next() {
		var (
			id        int64
			createdAt time.Time
			payload   []byte
		)
		if err := rows.Scan(&id, &createdAt, &payload); err != nil {
			return nil, err
		}
		rep, err := decodeReport(id, createdAt, payload)
		if err != nil {
			return nil, err
		}
		out = append(out, rep)
	}
	return out, rows.Err()
}

func decodeReport(id int64, createdAt time.Time, payload []byte) (*service.IdentifyReport, error) {
	var rep service.IdentifyReport
	if err := json.Unmarshal(payload, &rep); err != nil {
		return nil, fmt.Errorf("decode report %d: %w", id, err)
	}
	rep.ID = id
	if !createdAt.IsZero() {
		rep.CreatedAt = createdAt
	}
	return &rep, nil
}
