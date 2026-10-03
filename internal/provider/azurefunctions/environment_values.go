package azurefunctions

import "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"

type environmentValues struct{ values map[string]string }

func (v environmentValues) read(environment []*armappcontainers.EnvironmentVar) map[string]string {
	v.values = map[string]string{}
	v.collect(environment)
	return v.values
}
func (v environmentValues) collect(environment []*armappcontainers.EnvironmentVar) {
	for _, value := range environment {
		v.add(value)
	}
}
func (v environmentValues) add(environment *armappcontainers.EnvironmentVar) {
	value := v.value(environment)
	name := (optionalValue[string]{}).read(value.Name)
	text := (optionalValue[string]{}).read(value.Value) + (functionContract{}).separator() + (optionalValue[string]{}).read(value.SecretRef)
	v.store(environment != nil, name, text)
}
func (environmentValues) value(environment *armappcontainers.EnvironmentVar) armappcontainers.EnvironmentVar {
	if environment == nil {
		return armappcontainers.EnvironmentVar{}
	}
	return *environment
}
func (v environmentValues) store(present bool, name, value string) {
	if present {
		v.values[name] = value
	}
}
