// Package store persists every computation request and its result so
// history can be queried later. PostgreSQL is the production backend;
// an in-memory implementation backs unit tests and local runs.
package store

import (
	"context"
	"encoding/json"
	"time"
)

// Record is one persisted computation request/response pair.
type Record struct {
	ID        int64           `json:"id"`
	Type      string          `json:"type"` // "redshift" or "distance" (each batch line persists as one "distance" record)
	Request   json.RawMessage `json:"request"`
	Response  json.RawMessage `json:"response"`
	CreatedAt time.Time       `json:"created_at"`
}

// Filter narrows history queries.
type Filter struct {
	Type   string // empty = all types
	Limit  int    // <=0 means default
	Offset int
}

// Store is the persistence contract. Implementations must be safe for
// concurrent use.
type Store interface {
	Save(ctx context.Context, rec *Record) error
	List(ctx context.Context, f Filter) ([]Record, error)

	// Rest-line catalogs (registered tables; the built-in default
	// catalog lives in code and is not stored here).
	SaveCatalog(ctx context.Context, cat *CatalogRecord) error
	GetCatalog(ctx context.Context, id int64) (*CatalogRecord, error)
	ListCatalogs(ctx context.Context) ([]CatalogRecord, error)
	UpdateCatalog(ctx context.Context, cat *CatalogRecord) error

	// Identification reports, persisted for every completed
	// identification run (success or failure). They live apart from
	// the computation history above and must never leak into it.
	SaveReport(ctx context.Context, rep *ReportRecord) error
	GetReport(ctx context.Context, id int64) (*ReportRecord, error)
	ListReports(ctx context.Context, limit, offset int) ([]ReportRecord, error)

	Ping(ctx context.Context) error
	Close() error
}
