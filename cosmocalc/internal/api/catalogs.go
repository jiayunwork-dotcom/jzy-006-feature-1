package api

import (
	"net/http"

	"cosmocalc/internal/lines"
)

// catalogView is the external representation of a catalogue.
type catalogView struct {
	ID             string       `json:"id"`
	Name           string       `json:"name,omitempty"`
	WavelengthUnit string       `json:"wavelength_unit"`
	Source         lines.Source `json:"source"`
	LineCount      int          `json:"line_count"`
	Lines          []lines.Line `json:"lines"`
}

func toCatalogView(c *lines.Catalog) catalogView {
	return catalogView{
		ID:             c.ID,
		Name:           c.Name,
		WavelengthUnit: c.WavelengthUnit,
		Source:         c.Source,
		LineCount:      len(c.Lines),
		Lines:          c.Lines,
	}
}

// handleCreateCatalog registers a caller-supplied custom table. It
// survives restarts; the built-in id is reserved.
func (s *Server) handleCreateCatalog(w http.ResponseWriter, r *http.Request) {
	var in lines.CatalogInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "", err.Error())
		return
	}
	c, err := s.registry.Register(in)
	if err != nil {
		writeMappedErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"catalog": toCatalogView(c)})
}

// handleListCatalogs returns the built-in table plus every custom table.
func (s *Server) handleListCatalogs(w http.ResponseWriter, r *http.Request) {
	cats, err := s.registry.List()
	if err != nil {
		writeMappedErr(w, err)
		return
	}
	views := make([]catalogView, len(cats))
	for i, c := range cats {
		views[i] = toCatalogView(c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(views), "catalogs": views})
}

// handleGetCatalog fetches one table (built-in or custom).
func (s *Server) handleGetCatalog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := s.registry.Get(id)
	if err != nil {
		writeMappedErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"catalog": toCatalogView(c)})
}

// lineUpdateBody is the PATCH payload for correcting one rest wavelength.
type lineUpdateBody struct {
	RestWavelength *float64 `json:"rest_wavelength"`
}

// handleUpdateLine corrects a single line in a custom table. Edits take
// effect for the NEXT identification: runs already in flight keep their
// snapshot. The built-in table is read-only.
func (s *Server) handleUpdateLine(w http.ResponseWriter, r *http.Request) {
	catalogID := r.PathValue("id")
	lineID := r.PathValue("lineId")
	var body lineUpdateBody
	if err := decode(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "", err.Error())
		return
	}
	if body.RestWavelength == nil {
		writeErr(w, http.StatusBadRequest, "missing_field", "rest_wavelength",
			"rest_wavelength is required to update a line")
		return
	}
	c, err := s.registry.UpdateLine(catalogID, lineID, *body.RestWavelength)
	if err != nil {
		writeMappedErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"catalog": toCatalogView(c)})
}
