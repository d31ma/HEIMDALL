package azurefunctions

type tagComparison struct{}

func (c tagComparison) matches(actual, expected map[string]*string) bool {
	matches := true
	for key, value := range expected {
		matches = c.same(actual[key], value, matches)
	}
	return matches
}
func (tagComparison) same(actual, expected *string, previous bool) bool {
	actualValue := (optionalValue[string]{}).read(actual)
	expectedValue := (optionalValue[string]{}).read(expected)
	return (comparisonChecks{}).all([]bool{previous, actualValue == expectedValue})
}
