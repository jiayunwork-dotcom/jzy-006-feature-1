package api

import (
	"net/http"

	"cosmocalc/internal/calc"
	"cosmocalc/internal/lines"
	"cosmocalc/internal/service"
)

// mapIdentifyError translates every pre-matching/catalogue error into
// the structured body and HTTP status the API contract requires.
func mapIdentifyError(err error) (apiError, int) {
	if fe, ok := service.AsIdentifyFieldError(err); ok {
		return apiError{Type: fe.Type, Message: fe.Message, Field: fe.Field}, http.StatusBadRequest
	}
	if le, ok := lines.AsError(err); ok {
		status := http.StatusBadRequest
		switch le.Kind {
		case lines.ErrCatalogNotFound, lines.ErrLineNotFound:
			status = http.StatusNotFound
		case lines.ErrUnitMismatch:
			status = http.StatusBadRequest
		}
		return apiError{Type: string(le.Kind), Message: le.Message, Field: le.Field}, status
	}
	if ce, ok := service.AsCalcError(err); ok {
		status := http.StatusBadRequest
		if ce.Kind == calc.ErrBlueshiftDistance {
			status = http.StatusUnprocessableEntity
		}
		return apiError{Type: string(ce.Kind), Message: ce.Message, Field: ce.Field}, status
	}
	return apiError{Type: "internal", Message: err.Error()}, http.StatusInternalServerError
}

func writeMappedErr(w http.ResponseWriter, err error) {
	ae, status := mapIdentifyError(err)
	writeErr(w, status, ae.Type, ae.Field, ae.Message)
}
