package lambda

import "io"

type boundedBody struct {
	body      io.ReadCloser
	remaining int64
}

func (b *boundedBody) Read(buffer []byte) (int, error) {
	limited := b.limit(buffer)
	reader := b.reader()
	count, err := reader.Read(limited)
	b.remaining -= int64(count)
	return count, err
}

func (b *boundedBody) limit(buffer []byte) []byte {
	if int64(len(buffer)) > b.remaining {
		return buffer[:b.remaining]
	}
	return buffer
}

func (b *boundedBody) reader() io.Reader {
	if b.remaining == 0 {
		return overflowReader{body: b.body}
	}
	return b.body
}

func (b *boundedBody) Close() error { return b.body.Close() }
