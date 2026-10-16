package spec

import "errors"

type Function struct {
	RuntimeContract string `json:"runtime_contract"`
	Invocation      string `json:"invocation"`
	TimeoutMS       int    `json:"timeout_ms,omitempty"`
	Architecture    string `json:"architecture,omitempty"`
}

func (f Function) WireVersion() string { return "2" }

func (f Function) LambdaRuntime() string { return "lambda-runtime-api" }

func (f Function) AzureRuntime() string { return "azure-functions-v4" }

func (f Function) MinimumTimeoutMS() int { return 1000 }

func (f Function) MaximumTimeoutMS() int { return 900000 }

func (f Function) Validate() error {
	return errors.Join(f.runtimeError(), f.invocationError(), f.architectureError(), f.timeoutError())
}

func (f Function) runtimeError() error {
	if f.RuntimeContract == f.LambdaRuntime() || f.RuntimeContract == f.AzureRuntime() {
		return nil
	}
	return &FunctionContractError{Field: "runtime_contract", Message: "declare lambda-runtime-api or azure-functions-v4; ordinary container images are not function runtimes"}
}

func (f Function) invocationError() error {
	if f.RuntimeContract == f.LambdaRuntime() && f.Invocation == "json" || f.RuntimeContract == f.AzureRuntime() && f.Invocation == "http" {
		return nil
	}
	return &FunctionContractError{Field: "invocation", Message: "lambda-runtime-api requires json; azure-functions-v4 requires http"}
}

func (f Function) architectureError() error {
	if f.Architecture == "" || f.Architecture == "amd64" || f.Architecture == "arm64" {
		return nil
	}
	return &FunctionContractError{Field: "architecture", Message: "declare one Linux architecture: amd64 or arm64"}
}

func (f Function) timeoutError() error {
	if f.TimeoutMS == 0 || f.TimeoutMS >= f.MinimumTimeoutMS() && f.TimeoutMS <= f.MaximumTimeoutMS() && f.TimeoutMS%f.MinimumTimeoutMS() == 0 {
		return nil
	}
	return &FunctionContractError{Field: "timeout", Message: "use whole seconds from 1s through 900s; omit to use runtime configuration"}
}
