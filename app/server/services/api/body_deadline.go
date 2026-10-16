package api

import (
	"github.com/d31ma/heimdall/app/server/utils/sequence"
	"net/http"
	"time"
)

type bodyDeadline struct {
	writer http.ResponseWriter
	until  time.Time
}

func (d bodyDeadline) Start() error {
	return (sequence.Steps{Actions: d.steps(d.until)}).Run()
}
func (d bodyDeadline) Reset() error {
	return (sequence.Steps{Actions: d.steps(time.Time{})}).Run()
}
func (d bodyDeadline) steps(until time.Time) []func() error {
	if d.writer == nil {
		return nil
	}
	return []func() error{func() error { return http.NewResponseController(d.writer).SetReadDeadline(until) }}
}
