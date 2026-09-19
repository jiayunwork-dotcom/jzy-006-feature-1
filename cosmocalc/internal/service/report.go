package service

import (
	"time"

	"cosmocalc/internal/identify"
	"cosmocalc/internal/lines"
)

// IdentifyPeak is one observed peak submitted for identification. Only
// the observed wavelength is given; the rest wavelength and redshift are
// what the service has to find.
type IdentifyPeak struct {
	ObservedWavelength *float64 `json:"observed_wavelength,omitempty"`
}

// IdentifyRequest is a line-identification request. Exactly one table
// source must be supplied: CatalogID (a registered/built-in table) or
// Catalog (a one-shot inline table that only this run may use).
type IdentifyRequest struct {
	WavelengthUnit string              `json:"wavelength_unit"`
	Peaks          []IdentifyPeak      `json:"peaks"`
	CatalogID      string              `json:"catalog_id,omitempty"`
	Catalog        *lines.CatalogInput `json:"catalog,omitempty"`
	// HubbleConstant, when supplied and the identification is unique and
	// not a blueshift, additionally runs the existing Hubble-law distance
	// computation for the common redshift.
	HubbleConstant *float64 `json:"hubble_constant,omitempty"`
}

// SnapshotLine is one line as captured in a per-run table snapshot.
type SnapshotLine struct {
	ID             string  `json:"id"`
	Label          string  `json:"label,omitempty"`
	RestWavelength float64 `json:"rest_wavelength"`
	WavelengthUnit string  `json:"wavelength_unit"`
}

// SnapshotCatalog is the immutable table snapshot attached to a report.
type SnapshotCatalog struct {
	ID             string         `json:"id"`
	Name           string         `json:"name,omitempty"`
	WavelengthUnit string         `json:"wavelength_unit"`
	Source         lines.Source   `json:"source"`
	Lines          []SnapshotLine `json:"lines"`
}

// IdentifyReport is the persisted record of one identification attempt,
// including the table snapshot it ran against. Reports live in their own
// store and never appear in the redshift/distance computation history.
type IdentifyReport struct {
	ID                 int64                `json:"id"`
	CreatedAt          time.Time            `json:"created_at"`
	Status             identify.Status      `json:"status"`
	WavelengthUnit     string               `json:"wavelength_unit"`
	WindowKmS          float64              `json:"velocity_window_kms"`
	ObservedWavelength []float64            `json:"observed_wavelengths"`
	RequestedCatalogID string               `json:"requested_catalog_id"`
	HubbleConstant     *float64             `json:"hubble_constant,omitempty"`
	Snapshot           SnapshotCatalog      `json:"snapshot"`
	Candidates         []identify.Candidate `json:"candidates"`
	DistanceResult     *DistanceResult      `json:"distance,omitempty"`
	DistanceError      *ReportDistanceError `json:"distance_error,omitempty"`
}

// ReportDistanceError records why the optional distance query failed
// (e.g. a uniquely identified blueshift is never given a distance).
type ReportDistanceError struct {
	Type    string `json:"type"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// IdentifyResponse is the transport view of a report/replay.
type IdentifyResponse struct {
	ID                 int64                `json:"id"`
	CreatedAt          time.Time            `json:"created_at"`
	Status             identify.Status      `json:"status"`
	WavelengthUnit     string               `json:"wavelength_unit"`
	WindowKmS          float64              `json:"velocity_window_kms"`
	ObservedWavelength []float64            `json:"observed_wavelengths"`
	Snapshot           SnapshotCatalog      `json:"snapshot"`
	Candidate          *identify.Candidate  `json:"candidate,omitempty"`
	Candidates         []identify.Candidate `json:"candidates,omitempty"`
	Distance           *DistanceResult      `json:"distance,omitempty"`
	DistanceError      *ReportDistanceError `json:"distance_error,omitempty"`
	Replayed           bool                 `json:"replayed,omitempty"`
}
