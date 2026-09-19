package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"cosmocalc/internal/lines"
)

const catalogSchema = `
CREATE TABLE IF NOT EXISTS line_catalogs (
    id              TEXT PRIMARY KEY,
    name            TEXT        NOT NULL DEFAULT '',
    wavelength_unit TEXT        NOT NULL,
    lines           JSONB       NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS identification_reports (
    id         BIGSERIAL PRIMARY KEY,
    status     TEXT        NOT NULL,
    payload    JSONB       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS identification_reports_created_idx ON identification_reports (created_at DESC);
`

// SaveCatalog persists a custom catalogue as one row (lines serialised as
// JSON). Duplicate ids are rejected.
func (p *PostgresStore) SaveCatalog(ctx context.Context, c *lines.Catalog) error {
	linesJSON, err := json.Marshal(c.Lines)
	if err != nil {
		return err
	}
	_, err = p.db.ExecContext(ctx,
		`INSERT INTO line_catalogs (id, name, wavelength_unit, lines) VALUES ($1, $2, $3, $4)`,
		c.ID, c.Name, c.WavelengthUnit, linesJSON)
	if err != nil {
		if isUniqueViolation(err) {
			return &lines.Error{Kind: lines.ErrCatalogIDConflict, Field: "id",
				Message: "catalog id " + c.ID + " is already registered"}
		}
		return err
	}
	return nil
}

// GetCatalog loads one custom catalogue.
func (p *PostgresStore) GetCatalog(ctx context.Context, id string) (*lines.Catalog, error) {
	var (
		name, unit string
		linesJSON  []byte
	)
	err := p.db.QueryRowContext(ctx,
		`SELECT name, wavelength_unit, lines FROM line_catalogs WHERE id = $1`, id).
		Scan(&name, &unit, &linesJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &lines.Error{Kind: lines.ErrCatalogNotFound, Field: "catalog_id",
			Message: "no catalog registered with id " + id}
	}
	if err != nil {
		return nil, err
	}
	var ls []lines.Line
	if err := json.Unmarshal(linesJSON, &ls); err != nil {
		return nil, err
	}
	return &lines.Catalog{ID: id, Name: name, WavelengthUnit: unit, Source: lines.SourceCustom, Lines: ls}, nil
}

// ListCatalogs loads every custom catalogue ordered by id.
func (p *PostgresStore) ListCatalogs(ctx context.Context) ([]*lines.Catalog, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT id, name, wavelength_unit, lines FROM line_catalogs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*lines.Catalog{}
	for rows.Next() {
		var (
			id, name, unit string
			linesJSON      []byte
		)
		if err := rows.Scan(&id, &name, &unit, &linesJSON); err != nil {
			return nil, err
		}
		var ls []lines.Line
		if err := json.Unmarshal(linesJSON, &ls); err != nil {
			return nil, err
		}
		out = append(out, &lines.Catalog{ID: id, Name: name, WavelengthUnit: unit, Source: lines.SourceCustom, Lines: ls})
	}
	return out, rows.Err()
}

// UpdateLineWavelength rewrites one line's rest wavelength inside the
// catalogue's JSON line set.
func (p *PostgresStore) UpdateLineWavelength(ctx context.Context, catalogID, lineID string, wavelength float64) (*lines.Catalog, error) {
	c, err := p.GetCatalog(ctx, catalogID)
	if err != nil {
		return nil, err
	}
	found := false
	for i := range c.Lines {
		if c.Lines[i].ID == lineID {
			c.Lines[i].RestWavelength = wavelength
			found = true
			break
		}
	}
	if !found {
		return nil, &lines.Error{Kind: lines.ErrLineNotFound, Field: "line_id",
			Message: "catalog " + catalogID + " has no line with id " + lineID}
	}
	linesJSON, err := json.Marshal(c.Lines)
	if err != nil {
		return nil, err
	}
	if _, err := p.db.ExecContext(ctx,
		`UPDATE line_catalogs SET lines = $2, updated_at = now() WHERE id = $1`,
		catalogID, linesJSON); err != nil {
		return nil, err
	}
	return c, nil
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate key")
}
