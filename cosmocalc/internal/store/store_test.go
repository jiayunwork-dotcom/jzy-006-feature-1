package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func ctx() context.Context { return context.Background() }

// Catalogs register, read back, list and update; updates replace the
// lines and bump the update timestamp.
func TestMemStoreCatalogLifecycle(t *testing.T) {
	m := NewMemStore()
	cat := &CatalogRecord{Name: "custom", Unit: "nm", Lines: json.RawMessage(`[{"id":"A","rest_wavelength":500}]`)}
	if err := m.SaveCatalog(ctx(), cat); err != nil {
		t.Fatalf("save: %v", err)
	}
	if cat.ID != 1 || cat.CreatedAt.IsZero() || cat.UpdatedAt.IsZero() {
		t.Fatalf("save must assign id and timestamps: %+v", cat)
	}

	got, err := m.GetCatalog(ctx(), 1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "custom" || got.Unit != "nm" || string(got.Lines) == "" {
		t.Fatalf("unexpected catalog: %+v", got)
	}

	if _, err := m.GetCatalog(ctx(), 99); !errors.Is(err, ErrCatalogNotFound) {
		t.Fatalf("missing catalog: err = %v, want ErrCatalogNotFound", err)
	}

	cats, err := m.ListCatalogs(ctx())
	if err != nil || len(cats) != 1 {
		t.Fatalf("list: %v %v", cats, err)
	}

	upd := &CatalogRecord{ID: 1, Name: "custom-v2", Unit: "nm", Lines: json.RawMessage(`[{"id":"A","rest_wavelength":505}]`)}
	if err := m.UpdateCatalog(ctx(), upd); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = m.GetCatalog(ctx(), 1)
	if got.Name != "custom-v2" || string(got.Lines) != `[{"id":"A","rest_wavelength":505}]` {
		t.Fatalf("update not applied: %+v", got)
	}
	if !got.UpdatedAt.After(got.CreatedAt) && !got.UpdatedAt.Equal(got.CreatedAt) {
		t.Fatalf("updated_at must not go backwards: %+v", got)
	}
	if err := m.UpdateCatalog(ctx(), &CatalogRecord{ID: 99, Name: "x", Unit: "nm", Lines: json.RawMessage(`[]`)}); !errors.Is(err, ErrCatalogNotFound) {
		t.Fatalf("update missing: err = %v, want ErrCatalogNotFound", err)
	}
}

// GetCatalog/ListCatalogs hand out independent copies: mutating a
// returned record never touches the stored one. This is what makes
// per-run snapshots safe at the store boundary.
func TestMemStoreCatalogCopiesAreIndependent(t *testing.T) {
	m := NewMemStore()
	original := json.RawMessage(`[{"id":"A","rest_wavelength":500}]`)
	cat := &CatalogRecord{Name: "c", Unit: "nm", Lines: original}
	if err := m.SaveCatalog(ctx(), cat); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Mutating the caller's buffer after Save must not corrupt the store.
	original[20] = '9'

	got, _ := m.GetCatalog(ctx(), 1)
	got.Lines[20] = '7'
	again, _ := m.GetCatalog(ctx(), 1)
	if string(again.Lines) != `[{"id":"A","rest_wavelength":500}]` {
		t.Fatalf("stored lines were mutated through a returned copy: %s", again.Lines)
	}
}

// Reports persist and list newest-first with limit/offset, separately
// from computation records.
func TestMemStoreReportLifecycle(t *testing.T) {
	m := NewMemStore()
	for _, status := range []string{"identified", "ambiguous", "no_match"} {
		rep := &ReportRecord{
			Status:   status,
			Request:  json.RawMessage(`{"peaks":[1]}`),
			Snapshot: json.RawMessage(`{"unit":"nm"}`),
			Result:   json.RawMessage(`{}`),
		}
		if err := m.SaveReport(ctx(), rep); err != nil {
			t.Fatalf("save report: %v", err)
		}
	}
	got, err := m.GetReport(ctx(), 2)
	if err != nil || got.Status != "ambiguous" {
		t.Fatalf("get report: %v %+v", err, got)
	}
	if _, err := m.GetReport(ctx(), 42); !errors.Is(err, ErrReportNotFound) {
		t.Fatalf("missing report: err = %v, want ErrReportNotFound", err)
	}

	all, err := m.ListReports(ctx(), 0, 0)
	if err != nil || len(all) != 3 {
		t.Fatalf("list: %v %v", all, err)
	}
	if all[0].Status != "no_match" || all[2].Status != "identified" {
		t.Fatalf("reports must list newest first: %+v", all)
	}
	page, _ := m.ListReports(ctx(), 1, 1)
	if len(page) != 1 || page[0].Status != "ambiguous" {
		t.Fatalf("limit/offset paging wrong: %+v", page)
	}

	// Reports are not computation records: history stays empty.
	recs, _ := m.List(ctx(), Filter{})
	if len(recs) != 0 {
		t.Fatalf("reports must not leak into computation history: %+v", recs)
	}
}
