package api

type responseWritten struct{}

func (*responseWritten) Error() string { return "response already written" }
