package lines

import "testing"

type fakeRepo struct {
	catalogs map[string]*Catalog
}

func newFakeRepo() *fakeRepo { return &fakeRepo{catalogs: map[string]*Catalog{}} }

func (f *fakeRepo) SaveCatalog(c *Catalog) error {
	if _, ok := f.catalogs[c.ID]; ok {
		return &Error{Kind: ErrCatalogIDConflict, Field: "id", Message: "dup"}
	}
	f.catalogs[c.ID] = c.Clone()
	return nil
}
func (f *fakeRepo) GetCatalog(id string) (*Catalog, error) {
	c, ok := f.catalogs[id]
	if !ok {
		return nil, &Error{Kind: ErrCatalogNotFound, Field: "catalog_id", Message: "missing"}
	}
	return c.Clone(), nil
}
func (f *fakeRepo) ListCatalogs() ([]*Catalog, error) {
	out := make([]*Catalog, 0, len(f.catalogs))
	for _, c := range f.catalogs {
		out = append(out, c.Clone())
	}
	return out, nil
}
func (f *fakeRepo) UpdateLineWavelength(catalogID, lineID string, wavelength float64) (*Catalog, error) {
	c, ok := f.catalogs[catalogID]
	if !ok {
		return nil, &Error{Kind: ErrCatalogNotFound, Field: "catalog_id", Message: "missing"}
	}
	for i := range c.Lines {
		if c.Lines[i].ID == lineID {
			c.Lines[i].RestWavelength = wavelength
			return c.Clone(), nil
		}
	}
	return nil, &Error{Kind: ErrLineNotFound, Field: "line_id", Message: "missing line"}
}

func TestRegistry_RegisterGetUpdateAndBuiltin(t *testing.T) {
	reg := NewRegistry(newFakeRepo())

	// Built-in resolves through the registry without storage.
	got, err := reg.Get(DefaultCatalogID)
	if err != nil || len(got.Lines) == 0 {
		t.Fatalf("builtin lookup failed: %v", err)
	}

	c, err := reg.Register(CatalogInput{ID: "mine", WavelengthUnit: "nm", Lines: []LineInput{
		{ID: "A", RestWavelength: 100},
	}})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if c.Source != SourceCustom {
		t.Fatalf("source = %v, want custom", c.Source)
	}

	// The built-in id is reserved.
	if _, err := reg.Register(CatalogInput{ID: DefaultCatalogID, WavelengthUnit: "nm",
		Lines: []LineInput{{ID: "A", RestWavelength: 1}}}); err == nil {
		t.Fatal("registering under the built-in id must fail")
	}
	// Duplicate custom id fails.
	if _, err := reg.Register(CatalogInput{ID: "mine", WavelengthUnit: "nm",
		Lines: []LineInput{{ID: "A", RestWavelength: 1}}}); err == nil {
		t.Fatal("duplicate custom id must fail")
	}
	// Unknown id is a structured not-found.
	if _, err := reg.Get("nope"); err == nil {
		t.Fatal("unknown catalog must fail")
	}

	// Built-in table cannot be edited.
	if _, err := reg.UpdateLine(DefaultCatalogID, "HI_HA", 660); err == nil {
		t.Fatal("builtin table must be read-only")
	}

	updated, err := reg.UpdateLine("mine", "A", 110)
	if err != nil {
		t.Fatalf("update line: %v", err)
	}
	ln, _ := updated.LineByID("A")
	if ln.RestWavelength != 110 {
		t.Fatalf("updated wavelength = %v, want 110", ln.RestWavelength)
	}
	// Persisted read reflects the correction.
	again, _ := reg.Get("mine")
	ln2, _ := again.LineByID("A")
	if ln2.RestWavelength != 110 {
		t.Fatalf("stored wavelength = %v, want 110", ln2.RestWavelength)
	}

	// Updating an unknown line/catalog fails with distinct kinds.
	if _, err := reg.UpdateLine("mine", "ZZZ", 1); err == nil {
		t.Fatal("unknown line must fail")
	}
	if _, err := reg.UpdateLine("nope", "A", 1); err == nil {
		t.Fatal("unknown catalog must fail")
	}
}

func TestRegistry_ListIncludesBuiltin(t *testing.T) {
	reg := NewRegistry(newFakeRepo())
	all, err := reg.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 || all[0].ID != DefaultCatalogID {
		t.Fatalf("fresh registry should list only the built-in table, got %d", len(all))
	}
}
