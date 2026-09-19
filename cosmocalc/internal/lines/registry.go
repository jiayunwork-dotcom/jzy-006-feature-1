package lines

// Registry resolves the table used by an identification: the built-in
// table, a previously registered custom table, or a one-shot inline
// table. Custom tables are persisted and therefore survive restarts.
type Registry struct {
	repo CatalogRepository
}

// NewRegistry builds a registry on top of a custom-catalogue repository.
// repo may be nil, in which case only the built-in and inline tables
// resolve.
func NewRegistry(repo CatalogRepository) *Registry {
	return &Registry{repo: repo}
}

// Get resolves a registered table by id: the built-in id always resolves
// to the shipped table; anything else is looked up among custom tables.
func (r *Registry) Get(id string) (*Catalog, error) {
	if id == DefaultCatalogID {
		return BuiltinCatalog(), nil
	}
	if r.repo == nil {
		return nil, &Error{Kind: ErrCatalogNotFound, Field: "catalog_id",
			Message: "no catalog registered with id " + id}
	}
	c, err := r.repo.GetCatalog(id)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// List returns the built-in table plus every registered custom table.
func (r *Registry) List() ([]*Catalog, error) {
	out := []*Catalog{BuiltinCatalog()}
	if r.repo != nil {
		custom, err := r.repo.ListCatalogs()
		if err != nil {
			return nil, err
		}
		out = append(out, custom...)
	}
	return out, nil
}

// Register validates and persists a new custom table.
func (r *Registry) Register(in CatalogInput) (*Catalog, error) {
	c, err := FromInput(in, SourceCustom)
	if err != nil {
		return nil, err
	}
	if c.ID == DefaultCatalogID {
		return nil, &Error{Kind: ErrCatalogIDConflict, Field: "id",
			Message: "catalog id " + DefaultCatalogID + " is reserved for the built-in table"}
	}
	if r.repo == nil {
		return nil, &Error{Kind: ErrCatalogNotFound, Field: "id",
			Message: "persistent catalog storage is not available"}
	}
	if err := r.repo.SaveCatalog(c); err != nil {
		return nil, err
	}
	return c.Clone(), nil
}

// UpdateLine corrects one line's rest wavelength in a custom table. The
// built-in table is immutable.
func (r *Registry) UpdateLine(catalogID, lineID string, wavelength float64) (*Catalog, error) {
	if catalogID == DefaultCatalogID {
		return nil, &Error{Kind: ErrBuiltinCatalogReadOnly, Field: "catalog_id",
			Message: "the built-in table is read-only; register a custom table to edit line wavelengths"}
	}
	if !isPositiveFinite(wavelength) {
		return nil, &Error{Kind: ErrNonPositiveLineWavelength, Field: "rest_wavelength",
			Message: "rest wavelength must be positive and finite"}
	}
	if r.repo == nil {
		return nil, &Error{Kind: ErrCatalogNotFound, Field: "catalog_id",
			Message: "no catalog registered with id " + catalogID}
	}
	return r.repo.UpdateLineWavelength(catalogID, lineID, wavelength)
}
