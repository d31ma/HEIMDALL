package diff

import "github.com/d31ma/heimdall/app/server/services/provider"

type ReportInput struct {
	App             provider.AppRef
	Target          string
	DesiredRevision string
	Plan            provider.Plan
	Live            provider.LiveState
	LiveReadFailed  bool
	Services        []ServiceDiff
}
