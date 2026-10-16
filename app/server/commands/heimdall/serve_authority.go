package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/d31ma/heimdall/app/server/services/auth"
)

type serveAuthority struct {
	storage *serveStorage
	context context.Context
	engine  *auth.Engine
	failure error
}

func (a *serveAuthority) Run() error {
	result := (serveDoctor{}).Run(a.storage.settings.loaded.SesameDir)
	action := a.doctorAction(result)
	return action()
}

func (a *serveAuthority) doctorAction(result serveDoctorResult) func() error {
	if result.failure != nil {
		return result.Failure
	}
	return a.start
}

func (a *serveAuthority) start() error {
	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a.context = signalContext
	a.engine, a.failure = auth.Start(signalContext, auth.Options{
		Deployment: a.storage.settings.loaded.SesameDir,
		TenantID:   a.storage.settings.loaded.TenantID,
		Stderr:     os.Stderr,
	})
	action := a.engineAction()
	return action()
}

func (a *serveAuthority) engineAction() func() error {
	if a.failure != nil {
		return a.failed
	}
	return a.startApplication
}

func (a *serveAuthority) startApplication() error {
	defer func() { _ = a.engine.Close() }()
	application := serveApplication{authority: a}
	return application.Run()
}

func (a *serveAuthority) failed() error {
	return a.failure
}
