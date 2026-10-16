package azurefunctions

type scaleComparison struct{}

func (c scaleComparison) matches(scale *nativeScale) bool {
	value := c.value(scale)
	return (comparisonChecks{}).all([]bool{scale != nil, (optionalValue[int32]{}).read(value.MinReplicas) == 0, (optionalValue[int32]{}).read(value.MaxReplicas) == (functionContract{}).maximumReplicas(), !(optionalValue[bool]{}).read(value.AllowScalingRuleOverride)})
}
func (scaleComparison) value(scale *nativeScale) nativeScale {
	if scale == nil {
		return nativeScale{}
	}
	return *scale
}
