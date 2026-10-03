package lambda

import "io"

type overflowReader struct{ body io.Reader }

func (r overflowReader) Read(_ []byte) (int, error) {
	var extra [1]byte
	count, err := r.body.Read(extra[:])
	return 0, r.result(count, err)
}

func (r overflowReader) result(count int, err error) error {
	if count > 0 {
		return (Diagnostic{}).Format((Diagnostic{}).ResponseLimitCode(), "Lambda response exceeds bounded payload limit")
	}
	return err
}
