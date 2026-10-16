package lambda

import (
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"maps"
)

type publicationEnvironment struct{}

func (comparison publicationEnvironment) Matches(left, right *types.FunctionConfiguration) bool {
	desired, actual := comparison.values(left), comparison.values(right)
	return maps.Equal(desired, actual)
}
func (publicationEnvironment) values(configuration *types.FunctionConfiguration) map[string]string {
	if configuration == nil || configuration.Environment == nil {
		return nil
	}
	return configuration.Environment.Variables
}
