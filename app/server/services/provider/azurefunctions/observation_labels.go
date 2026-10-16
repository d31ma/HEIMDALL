package azurefunctions

type observationLabels struct{}

func (observationLabels) merge(current, next string, first bool) string {
	if first || current == next {
		return next
	}
	return ""
}
