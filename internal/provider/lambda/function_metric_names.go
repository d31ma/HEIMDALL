package lambda

type functionMetricNames struct{}

func (functionMetricNames) InvocationsID() string { return "invocations" }
func (functionMetricNames) ErrorsID() string      { return "errors" }
func (functionMetricNames) ThrottlesID() string   { return "throttles" }
func (functionMetricNames) DurationID() string    { return "duration" }
func (functionMetricNames) Sum() string           { return "Sum" }
func (functionMetricNames) Average() string       { return "Average" }

func (functionMetricNames) Count() int { return 4 }
