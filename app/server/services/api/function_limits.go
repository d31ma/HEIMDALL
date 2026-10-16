package api

import "time"

type functionLimits struct{}

func (functionLimits) PayloadBytes() int           { return 256 * 1024 }
func (functionLimits) EnvelopeBytes() int          { return 384 * 1024 }
func (functionLimits) WaitDuration() time.Duration { return 45 * time.Second }
