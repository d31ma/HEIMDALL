package azurefunctions

type comparisonChecks struct{}

func (comparisonChecks) all(checks []bool) bool {
	matches := true
	for index := 0; index < len(checks) && matches; index++ {
		matches = checks[index]
	}
	return matches
}
