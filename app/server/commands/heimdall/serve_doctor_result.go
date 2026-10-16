package main

import (
	"fmt"
	"strings"
)

type serveDoctorResult struct {
	output  []byte
	failure error
}

func (r serveDoctorResult) RootLocked() bool {
	text := string(r.output) + " " + r.failure.Error()
	return strings.Contains(text, "EROOTLOCKED")
}

func (r serveDoctorResult) Failure() error {
	return fmt.Errorf("HD0113: sesame doctor: %w: %s", r.failure, r.output)
}
