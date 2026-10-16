package api

import (
	"errors"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
	"io"
)

type boundedLog struct {
	Reader io.ReadCloser
	Limit  int
	data   []byte
}

func (b *boundedLog) Read(prior error) ([]byte, error) {
	readErr := (sequence.Steps{Actions: []func() error{func() error { return prior }, b.available, b.read}}).Run()
	closeErr := b.close()
	boundErr := b.bound()
	return b.data, errors.Join(readErr, closeErr, boundErr)
}
func (b *boundedLog) available() error {
	if b.Reader == nil {
		return &boundaryError{Code: "HD0500", Message: "the function log reader is unavailable"}
	}
	return nil
}
func (b *boundedLog) read() error {
	data, err := io.ReadAll(io.LimitReader(b.Reader, int64(b.Limit)+1))
	b.data = data
	return err
}
func (b *boundedLog) close() error {
	if b.Reader == nil {
		return nil
	}
	return b.Reader.Close()
}
func (b *boundedLog) bound() error {
	if len(b.data) > b.Limit {
		return &boundaryError{Code: (boundaryError{}).BadRequestCode(), Message: "function log output exceeds the byte limit"}
	}
	return nil
}
