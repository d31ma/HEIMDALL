package main

import (
	"log/slog"
	"time"
)

type serveStandby struct {
	deployment string
	logger     *slog.Logger
	result     serveDoctorResult
	retry      bool
	failure    error
}

func (s *serveStandby) Wait(enabled bool) error {
	action := s.modeAction(enabled)
	return action()
}

func (s *serveStandby) modeAction(enabled bool) func() error {
	if enabled {
		return s.wait
	}
	return s.finished
}

func (s *serveStandby) wait() error {
	s.retry = true
	for s.retry {
		s.probe()
	}
	return s.failure
}

func (s *serveStandby) probe() {
	s.result = (serveDoctor{}).Run(s.deployment)
	action := s.probeAction()
	action()
}

func (s *serveStandby) probeAction() func() {
	if s.result.failure == nil {
		return s.takeOver
	}
	return s.readLock
}

func (s *serveStandby) takeOver() {
	s.logger.Info("standby: the deployment is free; taking over")
	s.retry = false
}

func (s *serveStandby) readLock() {
	locked := s.result.RootLocked()
	action := s.lockAction(locked)
	action()
}

func (s *serveStandby) lockAction(locked bool) func() {
	if locked {
		return s.waitForOwner
	}
	return s.failed
}

func (s *serveStandby) waitForOwner() {
	s.logger.Info("standby: an active control plane holds the deployment; waiting", "retry_in", "5s")
	time.Sleep(5 * time.Second)
}

func (s *serveStandby) failed() {
	s.failure = s.result.Failure()
	s.retry = false
}

func (s *serveStandby) finished() error {
	return s.failure
}
