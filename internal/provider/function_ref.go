package provider

type FunctionRef struct {
	AppRef
	Service string `json:"service"`
	Version string `json:"version"`
}
