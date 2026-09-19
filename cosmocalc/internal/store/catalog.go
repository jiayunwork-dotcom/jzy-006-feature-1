package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ErrCatalogNotFound is returned when a catalog ID does not exist.
var ErrCatalogNotFound = errors.New("store: catalog not found")

// ErrReportNotFound is returned when a report ID does not exist.
var ErrReportNotFound = errors.New("store: report not found")

// CatalogRecord is a registered rest-line catalog. Lines holds the
// serialized []lines.Line array; the store stays agnostic of the
// domain type, mirroring how Record keeps request/response payloads.
type CatalogRecord struct {
	ID        int64           `json:"id"`
	Name      string          `json:"name"`
	Unit      string          `json:"unit"`
	Lines     json.RawMessage `json:"lines"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func cloneRaw(b json.RawMessage) json.RawMessage {
	if b == nil {
		return nil
	}
	c := make(json.RawMessage, len(b))
	copy(c, b)
	return c
}

// ---- in-memory implementation ----

// SaveCatalog appends a catalog, assigning ID and timestamps.
func (m *MemStore) SaveCatalog(_ context.Context, cat *CatalogRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cat.ID = m.nextCatID
	m.nextCatID++
	now := time.Now().UTC()
	cat.CreatedAt = now
	cat.UpdatedAt = now
	stored := *cat
	stored.Lines = cloneRaw(cat.Lines)
	m.catalogs = append(m.catalogs, stored)
	return nil
}

// GetCatalog returns an independent copy of the catalog — callers can
// treat it as a snapshot even if the stored record is later updated.
func (m *MemStore) GetCatalog(_ context.Context, id int64) (*CatalogRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.catalogs {
		if c.ID == id {
			out := c
			out.Lines = cloneRaw(c.Lines)
			return &out, nil
		}
	}
	return nil, ErrCatalogNotFound
}

// ListCatalogs returns all registered catalogs, ascending by ID.
func (m *MemStore) ListCatalogs(_ context.Context) ([]CatalogRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]CatalogRecord, 0, len(m.catalogs))
	for _, c := range m.catalogs {
		c.Lines = cloneRaw(c.Lines)
		out = append(out, c)
	}
	return out, nil
}

// UpdateCatalog replaces name/unit/lines of an existing catalog and
// bumps its update timestamp. Runs that already snapshotted the
// catalog are unaffected; the next identification sees the new table.
func (m *MemStore) UpdateCatalog(_ context.Context, cat *CatalogRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, c := range m.catalogs {
		if c.ID == cat.ID {
			m.catalogs[i].Name = cat.Name
			m.catalogs[i].Unit = cat.Unit
			m.catalogs[i].Lines = cloneRaw(cat.Lines)
			m.catalogs[i].UpdatedAt = time.Now().UTC()
			*cat = m.catalogs[i]
			cat.Lines = cloneRaw(m.catalogs[i].Lines)
			return nil
		}
	}
	return ErrCatalogNotFound
}

// ---- PostgreSQL implementation ----

// SaveCatalog inserts a catalog and fills in its ID and timestamps.
func (p *PostgresStore) SaveCatalog(ctx context.Context, cat *CatalogRecord) error {
	row := p.db.QueryRowContext(ctx,
		`INSERT INTO line_catalogs (name, unit, lines) VALUES ($1, $2, $3)
		 RETURNING id, created_at, updated_at`,
		cat.Name, cat.Unit, cat.Lines)
	return row.Scan(&cat.ID, &cat.CreatedAt, &cat.UpdatedAt)
}

// GetCatalog fetches one catalog by ID.
func (p *PostgresStore) GetCatalog(ctx context.Context, id int64) (*CatalogRecord, error) {
	var c CatalogRecord
	row := p.db.QueryRowContext(ctx,
		`SELECT id, name, unit, lines, created_at, updated_at FROM line_catalogs WHERE id = $1`, id)
	if err := row.Scan(&c.ID, &c.Name, &c.Unit, &c.Lines, &c.CreatedAt, &c.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCatalogNotFound
		}
		return nil, err
	}
	return &c, nil
}

// ListCatalogs returns all registered catalogs, ascending by ID.
func (p *PostgresStore) ListCatalogs(ctx context.Context) ([]CatalogRecord, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT id, name, unit, lines, created_at, updated_at FROM line_catalogs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CatalogRecord{}
	for rows.Next() {
		var c CatalogRecord
		if err := rows.Scan(&c.ID, &c.Name, &c.Unit, &c.Lines, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateCatalog replaces name/unit/lines of an existing catalog.
func (p *PostgresStore) UpdateCatalog(ctx context.Context, cat *CatalogRecord) error {
	row := p.db.QueryRowContext(ctx,
		`UPDATE line_catalogs SET name = $1, unit = $2, lines = $3, updated_at = now()
		 WHERE id = $4 RETURNING created_at, updated_at`,
		cat.Name, cat.Unit, cat.Lines, cat.ID)
	if err := row.Scan(&cat.CreatedAt, &cat.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrCatalogNotFound
		}
		return err
	}
	return nil
}
