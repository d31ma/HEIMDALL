package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/d31ma/heimdall/internal/provider"
)

type failureResponse struct{}

func (f failureResponse) Write(writer http.ResponseWriter, err error) {
	var diagnostic provider.DiagnosticError
	known := errors.As(err, &diagnostic)
	action := f.action(writer, err, known)
	action()
}
func (f failureResponse) action(writer http.ResponseWriter, err error, known bool) func() {
	if known {
		return func() { (functionError{}).Write(writer, err) }
	}
	return func() { f.legacy(writer, err.Error()) }
}
func (failureResponse) legacy(writer http.ResponseWriter, message string) {
	switch {
	case strings.Contains(message, "HD0300"):
		writeJSON(writer, http.StatusUnprocessableEntity, ErrorResponse{Code: "HD0300", Message: message})
	case strings.HasPrefix(message, "HD02"):
		writeJSON(writer, http.StatusBadRequest, ErrorResponse{Code: codeOf(message), Message: message})
	default:
		writeJSON(writer, http.StatusInternalServerError, ErrorResponse{Code: "HD0500", Message: "the operation could not be completed"})
	}
}
