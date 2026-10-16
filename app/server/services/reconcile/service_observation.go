package reconcile

import "github.com/d31ma/heimdall/app/server/services/provider"

type ServiceObservation struct {
	AppID   string
	Service string
	Version string
	Window  provider.Window
	Filter  provider.LogFilter
}
