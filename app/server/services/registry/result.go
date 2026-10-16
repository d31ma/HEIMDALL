package registry

import "github.com/d31ma/heimdall/app/server/services/provider"

type Result struct {
	Commit  string               `json:"commit"`
	Changes []provider.Operation `json:"changes"`
	Skipped int                  `json:"skipped"`
}
