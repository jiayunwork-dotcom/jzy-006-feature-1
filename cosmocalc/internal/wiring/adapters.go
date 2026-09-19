// Package wiring adapts the context-aware PostgreSQL repositories to the
// context-free repository ports declared by the lines and service
// packages, and holds the construction of the feature dependencies.
package wiring

import (
	"context"
	"time"

	"cosmocalc/internal/lines"
	"cosmocalc/internal/service"
)

// CatalogStore is the context-aware persistence surface implemented by the
// PostgreSQL backend (the in-memory backend provides matching methods
// through MemAdapters).
type CatalogStore interface {
	SaveCatalog(ctx context.Context, c *lines.Catalog) error
	GetCatalog(ctx context.Context, id string) (*lines.Catalog, error)
	ListCatalogs(ctx context.Context) ([]*lines.Catalog, error)
	UpdateLineWavelength(ctx context.Context, catalogID, lineID string, wavelength float64) (*lines.Catalog, error)
}

// ReportStore is the context-aware persistence surface for reports.
type ReportStore interface {
	SaveReport(ctx context.Context, rep *service.IdentifyReport) error
	GetReport(ctx context.Context, id int64) (*service.IdentifyReport, error)
	ListReports(ctx context.Context, limit, offset int) ([]*service.IdentifyReport, error)
}

// CatalogAdapter turns a context-aware CatalogStore into the
// lines.CatalogRepository port using bounded background contexts.
type CatalogAdapter struct{ Store CatalogStore }

func (a CatalogAdapter) withCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// SaveCatalog implements lines.CatalogRepository.
func (a CatalogAdapter) SaveCatalog(c *lines.Catalog) error {
	ctx, cancel := a.withCtx()
	defer cancel()
	return a.Store.SaveCatalog(ctx, c)
}

// GetCatalog implements lines.CatalogRepository.
func (a CatalogAdapter) GetCatalog(id string) (*lines.Catalog, error) {
	ctx, cancel := a.withCtx()
	defer cancel()
	return a.Store.GetCatalog(ctx, id)
}

// ListCatalogs implements lines.CatalogRepository.
func (a CatalogAdapter) ListCatalogs() ([]*lines.Catalog, error) {
	ctx, cancel := a.withCtx()
	defer cancel()
	return a.Store.ListCatalogs(ctx)
}

// UpdateLineWavelength implements lines.CatalogRepository.
func (a CatalogAdapter) UpdateLineWavelength(catalogID, lineID string, wavelength float64) (*lines.Catalog, error) {
	ctx, cancel := a.withCtx()
	defer cancel()
	return a.Store.UpdateLineWavelength(ctx, catalogID, lineID, wavelength)
}

// ReportAdapter turns a context-aware ReportStore into the
// service.ReportRepository port.
type ReportAdapter struct{ Store ReportStore }

func (a ReportAdapter) withCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// SaveReport implements service.ReportRepository.
func (a ReportAdapter) SaveReport(rep *service.IdentifyReport) error {
	ctx, cancel := a.withCtx()
	defer cancel()
	return a.Store.SaveReport(ctx, rep)
}

// GetReport implements service.ReportRepository.
func (a ReportAdapter) GetReport(id int64) (*service.IdentifyReport, error) {
	ctx, cancel := a.withCtx()
	defer cancel()
	return a.Store.GetReport(ctx, id)
}

// ListReports implements service.ReportRepository.
func (a ReportAdapter) ListReports(limit, offset int) ([]*service.IdentifyReport, error) {
	ctx, cancel := a.withCtx()
	defer cancel()
	return a.Store.ListReports(ctx, limit, offset)
}
