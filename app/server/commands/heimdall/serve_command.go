package main

import (
	"log/slog"
	"os"
)

type serveCommand struct{}

func (c serveCommand) Run(arguments []string) error {
	configuration := serveConfiguration{}
	settings, failure := configuration.Load(arguments)
	action := c.configurationAction(settings, failure)
	return action()
}

func (c serveCommand) configurationAction(settings serveSettings, failure error) func() error {
	if failure != nil {
		return func() error { return failure }
	}
	return func() error { return c.start(settings) }
}

func (serveCommand) start(settings serveSettings) error {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	storage := serveStorage{settings: settings, logger: logger}
	return storage.Run()
}
