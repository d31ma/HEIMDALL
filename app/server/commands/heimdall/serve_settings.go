package main

import "time"

type serveSettings struct {
	deployment string
	loaded     config
	address    string
	retention  time.Duration
	standby    bool
}
