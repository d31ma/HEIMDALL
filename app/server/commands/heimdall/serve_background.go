package main

import (
	"context"
	"fmt"

	"github.com/d31ma/heimdall/app/server/services/observe"
)

type serveBackground struct {
	application    *serveApplication
	reconciliation *serveReconciliation
	collector      *observe.Collector
	failure        error
}

func (b *serveBackground) Run() error {
	b.reconciliation = &serveReconciliation{application: b.application}
	b.reconciliation.Prepare()
	backgroundContext, stop := context.WithCancel(context.Background())
	defer stop()
	b.reconciliation.Start(backgroundContext)
	b.startMetrics(backgroundContext)
	b.reconciliation.dispatcher.SetDrain(b.reconciliation.engine.Resume, b.reconciliation.engine.Expire)
	b.failure = b.reconciliation.engine.Repark()
	action := b.reparkAction()
	return action()
}

func (b *serveBackground) startMetrics(runContext context.Context) {
	storage := b.application.authority.storage
	b.collector = &observe.Collector{Store: storage.writer, Providers: b.reconciliation.engine.Providers, Logger: storage.logger, Retention: storage.settings.retention}
	go b.collector.Run(runContext)
}

func (b *serveBackground) reparkAction() func() error {
	if b.failure != nil {
		return b.failed
	}
	return b.listen
}

func (b *serveBackground) failed() error {
	return fmt.Errorf("HD0263: rebuild parked syncs: %w", b.failure)
}

func (b *serveBackground) listen() error {
	listener := serveListener{background: b}
	return listener.Listen(b.application.authority.context)
}
