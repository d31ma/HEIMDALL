package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/d31ma/heimdall/internal/sequence"
	"io"
	"net/http"
	"os"
	"time"
)

type strictBody struct {
	request *http.Request
	writer  http.ResponseWriter
	until   time.Time
	limit   int
	data    []byte
	decoder *json.Decoder
}

func (b *strictBody) Decode(into any) error {
	deadline := bodyDeadline{writer: b.writer, until: b.until}
	err := (sequence.Steps{Actions: []func() error{deadline.Start, b.read, b.bound, func() error { return b.decode(into) }, b.end}}).Run()
	return errors.Join(err, deadline.Reset())
}
func (b *strictBody) read() error {
	data, err := io.ReadAll(io.LimitReader(b.request.Body, int64(b.limit)+1))
	b.data = data
	return b.readResult(err)
}
func (b *strictBody) bound() error {
	if len(b.data) > b.limit {
		return &boundaryError{Code: (boundaryError{}).BadRequestCode(), Message: "request body exceeds the byte limit"}
	}
	return nil
}
func (b *strictBody) decode(into any) error {
	b.decoder = json.NewDecoder(bytes.NewReader(b.data))
	b.decoder.DisallowUnknownFields()
	err := b.decoder.Decode(into)
	return b.valid(err)
}
func (strictBody) valid(err error) error {
	if err != nil {
		return &boundaryError{Code: (boundaryError{}).BadRequestCode(), Message: "request body must be valid JSON with only declared fields"}
	}
	return nil
}
func (b *strictBody) end() error {
	var extra any
	err := b.decoder.Decode(&extra)
	if !errors.Is(err, io.EOF) {
		return &boundaryError{Code: (boundaryError{}).BadRequestCode(), Message: "request body must contain one JSON value"}
	}
	return nil
}

func (strictBody) readResult(err error) error {
	if os.IsTimeout(err) {
		return &boundaryError{Code: (boundaryError{}).BadRequestCode(), Message: "request body read deadline ended before invocation"}
	}
	return err
}
