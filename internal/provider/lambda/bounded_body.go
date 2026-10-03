package lambda

import (
	"io"
)

type boundedBody struct {
	body      io.ReadCloser
	remaining int64
}

func (b *boundedBody) Read(p []byte) (int, error) {
	return b.read(p)
}

func (b *boundedBody) read(p []byte) (int, error) {
	if b.remaining == 0 {
		var extra [1]byte
		n, err := b.body.Read(extra[:])
		if n > 0 {
			return 0, (Diagnostic{}).Format("HD0419: Lambda response exceeds bounded payload limit")
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.body.Read(p)
	b.remaining -= int64(n)
	return n, err
}

func (b *boundedBody) Close() error { return b.body.Close() }
