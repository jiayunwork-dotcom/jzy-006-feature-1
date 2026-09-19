package store

import (
	"sort"
	"sync"
	"time"

	"cosmocalc/internal/lines"
	"cosmocalc/internal/service"
)

// MemCatalogs is the in-memory custom-catalogue repository (tests and
// ephemeral runs). It returns deep copies and is safe for concurrent use.
// It implements lines.CatalogRepository.
type MemCatalogs struct {
	mu       sync.RWMutex
	catalogs map[string]*lines.Catalog
}

// NewMemCatalogs returns an empty catalogue repository.
func NewMemCatalogs() *MemCatalogs {
	return &MemCatalogs{catalogs: map[string]*lines.Catalog{}}
}

// SaveCatalog stores a custom catalogue, rejecting a duplicate id.
func (m *MemCatalogs) SaveCatalog(c *lines.Catalog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.catalogs[c.ID]; exists {
		return &lines.Error{Kind: lines.ErrCatalogIDConflict, Field: "id",
			Message: "catalog id " + c.ID + " is already registered"}
	}
	m.catalogs[c.ID] = c.Clone()
	return nil
}

// GetCatalog returns a deep copy of one custom catalogue.
func (m *MemCatalogs) GetCatalog(id string) (*lines.Catalog, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.catalogs[id]
	if !ok {
		return nil, &lines.Error{Kind: lines.ErrCatalogNotFound, Field: "catalog_id",
			Message: "no catalog registered with id " + id}
	}
	return c.Clone(), nil
}

// ListCatalogs returns deep copies of all custom catalogues, sorted by id.
func (m *MemCatalogs) ListCatalogs() ([]*lines.Catalog, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.catalogs))
	for id := range m.catalogs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]*lines.Catalog, 0, len(ids))
	for _, id := range ids {
		out = append(out, m.catalogs[id].Clone())
	}
	return out, nil
}

// UpdateLineWavelength corrects one line and returns the updated catalogue.
func (m *MemCatalogs) UpdateLineWavelength(catalogID, lineID string, wavelength float64) (*lines.Catalog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.catalogs[catalogID]
	if !ok {
		return nil, &lines.Error{Kind: lines.ErrCatalogNotFound, Field: "catalog_id",
			Message: "no catalog registered with id " + catalogID}
	}
	idx := -1
	for i, l := range c.Lines {
		if l.ID == lineID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, &lines.Error{Kind: lines.ErrLineNotFound, Field: "line_id",
			Message: "catalog " + catalogID + " has no line with id " + lineID}
	}
	updated := c.Clone()
	updated.Lines[idx].RestWavelength = wavelength
	m.catalogs[catalogID] = updated
	return updated.Clone(), nil
}

// MemReports is the in-memory identification-report repository. It
// implements service.ReportRepository.
type MemReports struct {
	mu      sync.RWMutex
	reports []*service.IdentifyReport
	nextID  int64
}

// NewMemReports returns an empty report repository.
func NewMemReports() *MemReports { return &MemReports{nextID: 1} }

// SaveReport appends a report, assigning id and timestamp.
func (m *MemReports) SaveReport(rep *service.IdentifyReport) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rep.ID = m.nextID
	m.nextID++
	rep.CreatedAt = time.Now().UTC()
	m.reports = append(m.reports, cloneReport(rep))
	return nil
}

// GetReport returns a deep copy of one report.
func (m *MemReports) GetReport(id int64) (*service.IdentifyReport, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, r := range m.reports {
		if r.ID == id {
			return cloneReport(r), nil
		}
	}
	return nil, ErrReportNotFound
}

// ListReports returns reports newest-first with limit/offset paging.
func (m *MemReports) ListReports(limit, offset int) ([]*service.IdentifyReport, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	out := make([]*service.IdentifyReport, 0, limit)
	for i := len(m.reports) - 1; i >= 0; i-- {
		if offset > 0 {
			offset--
			continue
		}
		if len(out) >= limit {
			break
		}
		out = append(out, cloneReport(m.reports[i]))
	}
	return out, nil
}
