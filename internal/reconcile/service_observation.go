package reconcile

import "github.com/d31ma/heimdall/internal/provider"

type ServiceObservation struct {
	AppID   string
	Service string
	Version string
	Window  provider.Window
	Filter  provider.LogFilter
}
