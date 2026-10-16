package lambda

import "time"

type functionLimits struct{}

func (functionLimits) MinimumMemory() int              { return 128 }
func (functionLimits) MaximumMemory() int              { return 10240 }
func (functionLimits) DefaultTimeout() int32           { return 3 }
func (functionLimits) PayloadBytes() int               { return 6 * 1024 * 1024 }
func (functionLimits) EnvironmentBytes() int           { return 4096 }
func (functionLimits) InventoryPages() int             { return 100 }
func (functionLimits) PageItems() int32                { return 50 }
func (functionLimits) QueryPages() int                 { return 20 }
func (functionLimits) LogStreams() int                 { return 100 }
func (functionLimits) MaximumLogLines() int            { return 2000 }
func (functionLimits) DefaultLogLines() int            { return 200 }
func (functionLimits) LogBytes() int                   { return 4 * 1024 * 1024 }
func (functionLimits) MetricPoints() int32             { return 10000 }
func (functionLimits) MetricWindow() time.Duration     { return 24 * time.Hour }
func (functionLimits) ReadTimeout() time.Duration      { return 30 * time.Second }
func (functionLimits) InventoryTimeout() time.Duration { return time.Minute }
func (functionLimits) ApplyTimeout() time.Duration     { return 10 * time.Minute }
func (functionLimits) InvokeTimeout() time.Duration    { return 905 * time.Second }
func (functionLimits) TransportTimeout() time.Duration { return 910 * time.Second }
func (functionLimits) PollDelay() time.Duration        { return 100 * time.Millisecond }
func (functionLimits) PruneWave() int                  { return 1 << 30 }
func (functionLimits) NameLength() int                 { return 51 }
func (functionLimits) NameDigestBytes() int            { return 6 }
