package main

import (
	"log/slog"
	"os"

	"github.com/d31ma/heimdall/app/server/repositories/store"
)

type serveStorage struct {
	settings serveSettings
	logger   *slog.Logger
	writer   *store.Store
	failure  error
}

func (s *serveStorage) Run() error {
	standby := serveStandby{deployment: s.settings.loaded.SesameDir, logger: s.logger}
	s.failure = standby.Wait(s.settings.standby)
	action := s.standbyAction()
	return action()
}

func (s *serveStorage) standbyAction() func() error {
	if s.failure != nil {
		return s.failed
	}
	return s.open
}

func (s *serveStorage) open() error {
	s.writer, s.failure = store.Open(s.settings.loaded.FyloRoot, os.Getenv("FYLO_BINARY"))
	action := s.storageAction()
	return action()
}

func (s *serveStorage) storageAction() func() error {
	if s.failure != nil {
		return s.failed
	}
	return s.startAuthority
}

func (s *serveStorage) startAuthority() error {
	defer func() { _ = s.writer.Close() }()
	authority := serveAuthority{storage: s}
	return authority.Run()
}

func (s *serveStorage) failed() error {
	return s.failure
}
