package lambda

import "github.com/aws/aws-sdk-go-v2/aws"

type nativePagination struct {
	token *string
	done  bool
	code  string
	noun  string
}

func (pagination *nativePagination) Advance(next *string, finished bool) error {
	return pagination.advance(next, finished)
}
func (pagination *nativePagination) advance(next *string, finished bool) error {
	if !finished && aws.ToString(next) == aws.ToString(pagination.token) {
		return (Diagnostic{}).Format(pagination.code, "native %s pagination did not advance", pagination.noun)
	}
	pagination.token, pagination.done = next, finished
	return nil
}
