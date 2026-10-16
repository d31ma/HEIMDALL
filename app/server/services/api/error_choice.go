package api

type errorChoice struct {
	Response ErrorResponse
	Status   int
	Matched  bool
}
