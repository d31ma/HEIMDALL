package main

import "slices"

type serveOptions struct {
	deployment string
	address    string
	standby    bool
}

func (o *serveOptions) Parse(arguments []string) {
	o.standby = slices.Contains(arguments, "--standby")
	parseFlags(arguments, map[string]*string{
		"--deployment": &o.deployment,
		"--addr":       &o.address,
	})
}
