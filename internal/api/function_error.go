package api

import (
	"context"
	"errors"
	"github.com/d31ma/heimdall/internal/provider"
	"net/http"
	"strings"
)

type functionError struct{}

func (f functionError) Write(writer http.ResponseWriter, err error) {
	var diagnostic provider.DiagnosticError
	known := errors.As(err, &diagnostic)
	code, message := f.details(diagnostic, known)
	choices := []errorChoice{f.timeout(err), {Response: ErrorResponse{Code: code, Message: message}, Status: f.status(code), Matched: known}, {Response: ErrorResponse{Code: "HD0500", Message: "the function operation could not be completed"}, Status: http.StatusInternalServerError, Matched: true}}
	choice := f.selectChoice(choices)
	writeJSON(writer, choice.Status, choice.Response)
}
func (functionError) details(diagnostic provider.DiagnosticError, known bool) (string, string) {
	if !known {
		return "", ""
	}
	return diagnostic.DiagnosticCode(), diagnostic.DiagnosticMessage()
}
func (functionError) selectChoice(choices []errorChoice) errorChoice {
	choice := errorChoice{}
	for index := 0; index < len(choices) && !choice.Matched; index++ {
		choice = choices[index]
	}
	return choice
}
func (functionError) timeout(err error) errorChoice {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return errorChoice{Response: ErrorResponse{Code: "HD0408", Message: "the wait for the function ended; execution may continue; do not retry automatically"}, Status: http.StatusGatewayTimeout, Matched: true}
	}
	return errorChoice{}
}
func (functionError) status(code string) int {
	switch {
	case code == "HD0284":
		return http.StatusConflict
	case code == "HD0503":
		return http.StatusServiceUnavailable
	case code == "HD0282" || code == "HD0283" || code == "HD0477":
		return http.StatusNotImplemented
	case strings.HasPrefix(code, "HD040") || code == "HD0410" || strings.HasPrefix(code, "HD028"):
		return http.StatusBadRequest
	case code == "HD0414":
		return http.StatusConflict
	}
	return http.StatusUnprocessableEntity
}
