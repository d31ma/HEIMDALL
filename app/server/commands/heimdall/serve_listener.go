package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/d31ma/heimdall/app/server/services/api"
)

type serveListener struct {
	background *serveBackground
	server     *http.Server
	publicURL  string
	failure    error
}

func (l *serveListener) Listen(signalContext context.Context) error {
	publicURL := l.publicURLAction()
	l.publicURL = publicURL()
	l.server = l.build()
	l.configureTLS()
	go l.shutdown(signalContext)
	l.log()
	serve := l.serveAction()
	l.failure = serve()
	action := l.resultAction()
	return action()
}

func (l *serveListener) publicURLAction() func() string {
	if l.background.application.authority.storage.settings.loaded.PublicURL == "" {
		return l.defaultURL
	}
	return l.configuredURL
}

func (l *serveListener) defaultURL() string {
	return defaultPublicURL(l.background.application.authority.storage.settings.address, useTLS())
}

func (l *serveListener) configuredURL() string {
	return l.background.application.authority.storage.settings.loaded.PublicURL
}

func (l *serveListener) build() *http.Server {
	application := l.background.application
	authority := application.authority
	storage := authority.storage
	reconciliation := l.background.reconciliation
	server := &api.Server{
		Engine:        authority.engine,
		Audit:         auditor{store: storage.writer, logger: storage.logger},
		Version:       version,
		Store:         storage.writer,
		Reconcile:     reconciliation.engine,
		Providers:     reconciliation.engine.Providers,
		Dispatcher:    reconciliation.dispatcher,
		Material:      application.material,
		PublicURL:     l.publicURL,
		Auto:          reconciliation.auto,
		Registry:      reconciliation.registry,
		Secrets:       reconciliation.engine.Secrets,
		Observe:       l.background.collector,
		DeviceEnabled: application.deviceEnabled,
	}
	return &http.Server{
		Addr:              storage.settings.address,
		Handler:           server.RateLimit(server.Handler()),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      3 * time.Minute,
	}
}

func (l *serveListener) configureTLS() {
	if useTLS() {
		l.server.TLSConfig = l.background.application.material.ServerTLSConfig()
	}
}

func (l *serveListener) shutdown(signalContext context.Context) {
	<-signalContext.Done()
	shutdownContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = l.server.Shutdown(shutdownContext)
}

func (l *serveListener) log() {
	storage := l.background.application.authority.storage
	storage.logger.Info("heimdall serving",
		"addr", storage.settings.address, "version", version, "tenant", storage.settings.loaded.TenantID,
		"tls", useTLS(), "public_url", l.publicURL,
		"fingerprint", l.background.application.material.ServerFingerprint())
}

func (l *serveListener) serveAction() func() error {
	if useTLS() {
		return l.listenTLS
	}
	return l.server.ListenAndServe
}

func (l *serveListener) listenTLS() error {
	return l.server.ListenAndServeTLS("", "")
}

func (l *serveListener) resultAction() func() error {
	if l.failure == nil {
		return l.finished
	}
	return l.closedResult
}

func (l *serveListener) finished() error {
	return nil
}

func (l *serveListener) closedResult() error {
	if errors.Is(l.failure, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("HD0114: listen: %w", l.failure)
}
