package provider

import (
	"context"
	"io"
)

type FunctionObserver interface {
	FunctionMetrics(context.Context, Target, FunctionRef, Window) (FunctionSeries, error)
	FunctionLogs(context.Context, Target, FunctionRef, LogFilter) (io.ReadCloser, error)
}
