package provider

type FunctionSeries struct {
	Ref         FunctionRef `json:"ref"`
	Invocations []Sample    `json:"invocations,omitempty"`
	Errors      []Sample    `json:"errors,omitempty"`
	Throttles   []Sample    `json:"throttles,omitempty"`
	DurationMS  []Sample    `json:"duration_ms,omitempty"`
}
