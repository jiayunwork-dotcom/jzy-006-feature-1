package lines

import (
	"testing"
)

// The shipped table must cover the three benchmark lines at exactly the
// laboratory wavelengths the golden example depends on.
func TestBuiltinCatalog_CoversBenchmarkLines(t *testing.T) {
	c := BuiltinCatalog()
	if c.WavelengthUnit != "nm" {
		t.Fatalf("builtin unit = %q, want nm", c.WavelengthUnit)
	}
	want := map[string]float64{
		"HI_HA":     656.28,
		"HI_HB":     486.13,
		"OIII_5007": 500.7,
	}
	for id, w := range want {
		ln, ok := c.LineByID(id)
		if !ok {
			t.Fatalf("builtin table missing line %s", id)
		}
		if ln.RestWavelength != w {
			t.Fatalf("line %s wavelength = %v, want %v", id, ln.RestWavelength, w)
		}
		if ln.WavelengthUnit != "nm" {
			t.Fatalf("line %s unit = %q, want nm", id, ln.WavelengthUnit)
		}
	}
	// And it must hold enough other lines that one peak hits multiple
	// redshifts.
	if len(c.Lines) < 10 {
		t.Fatalf("builtin table has %d lines, want at least 10", len(c.Lines))
	}
}

// BuiltinCatalog returns independent copies: mutating one must not leak
// into another retrieval.
func TestBuiltinCatalog_ImmutableCopies(t *testing.T) {
	a := BuiltinCatalog()
	a.Lines[0].RestWavelength = 1
	if b := BuiltinCatalog(); b.Lines[0].RestWavelength == 1 {
		t.Fatal("builtin catalog mutated through a returned copy")
	}
}

func TestFromInput_Validation(t *testing.T) {
	base := func() CatalogInput {
		return CatalogInput{ID: "c1", WavelengthUnit: "nm", Lines: []LineInput{
			{ID: "A", RestWavelength: 100},
			{ID: "B", RestWavelength: 200},
		}}
	}
	cases := []struct {
		name string
		mut  func(*CatalogInput)
		want ErrorKind
	}{
		{"missing unit", func(c *CatalogInput) { c.WavelengthUnit = "" }, ErrMissingWavelengthUnit},
		{"empty lines", func(c *CatalogInput) { c.Lines = nil }, ErrEmptyCatalog},
		{"missing line id", func(c *CatalogInput) { c.Lines[0].ID = " " }, ErrMissingLineID},
		{"duplicate line id", func(c *CatalogInput) { c.Lines[1].ID = "A" }, ErrDuplicateLineID},
		{"zero wavelength", func(c *CatalogInput) { c.Lines[0].RestWavelength = 0 }, ErrNonPositiveLineWavelength},
		{"negative wavelength", func(c *CatalogInput) { c.Lines[1].RestWavelength = -3 }, ErrNonPositiveLineWavelength},
		{"registered catalog without id", func(c *CatalogInput) { c.ID = "" }, ErrMissingCatalogID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base()
			tc.mut(&in)
			_, err := FromInput(in, SourceCustom)
			le, ok := err.(*Error)
			if !ok || le.Kind != tc.want {
				t.Fatalf("err = %v, want kind %v", err, tc.want)
			}
		})
	}
}

// An inline table is allowed to omit the catalogue id and is tagged as
// inline; lines inherit the declared unit.
func TestFromInput_Inline(t *testing.T) {
	c, err := FromInput(CatalogInput{WavelengthUnit: "Angstrom", Lines: []LineInput{
		{ID: "X", RestWavelength: 6562.8},
	}}, SourceInline)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Source != SourceInline {
		t.Fatalf("source = %v, want inline", c.Source)
	}
	if c.Lines[0].WavelengthUnit != "Angstrom" {
		t.Fatalf("line unit = %q, want Angstrom", c.Lines[0].WavelengthUnit)
	}
}
