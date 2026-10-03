package diff

import (
	"fmt"
	"github.com/d31ma/heimdall/internal/spec"
)

type functionDifference struct {
	desired *spec.Function
	live    *spec.Function
}

func (d functionDifference) Changes() []Change {
	desired, live := d.text(d.desired), d.text(d.live)
	return d.compare(desired, live)
}
func (functionDifference) text(function *spec.Function) string {
	if function == nil {
		return ""
	}
	return fmt.Sprintf("%s; invocation=%s; timeout_ms=%d; architecture=%s", function.RuntimeContract, function.Invocation, function.TimeoutMS, function.Architecture)
}
func (functionDifference) compare(desired, live string) []Change {
	if desired == live {
		return nil
	}
	return []Change{{Field: "function", Kind: Modified, Desired: desired, Live: live}}
}
