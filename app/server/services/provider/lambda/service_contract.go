package lambda

import (
	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
	"regexp"
	"strings"
)

type serviceContract struct {
	target  provider.Target
	service spec.Service
}

func (c serviceContract) Validate() error {
	return (sequence.Steps{Actions: []func() error{c.runtime, c.function, c.image, c.rollout, c.replicas, c.resources, c.environment}}).Run()
}
func (c serviceContract) runtime() error {
	if c.service.Function == nil || c.service.Function.RuntimeContract != "lambda-runtime-api" || c.service.Function.Invocation != "json" {
		return c.reject(provider.FeatureFunction, "declare lambda-runtime-api with direct json invocation")
	}
	return nil
}
func (c serviceContract) function() error { return c.service.Function.Validate() }
func (c serviceContract) image() error {
	pattern := regexp.MustCompile(`^[0-9]{12}\.dkr\.ecr\.` + regexp.QuoteMeta(c.target.Region) + `\.amazonaws\.com(?:\.cn)?/[a-z0-9._/-]+@sha256:[a-f0-9]{64}$`)
	if !pattern.MatchString(c.service.Image) {
		return c.reject(provider.FeatureFunction, "use an immutable same-region ECR image digest implementing the Lambda Runtime API")
	}
	return nil
}
func (c serviceContract) rollout() error {
	if c.service.Update != nil {
		return c.reject(provider.FeatureStopFirst, "container rollout directives do not describe Lambda version publication")
	}
	return nil
}
func (c serviceContract) replicas() error {
	if c.service.Replicas != 0 && c.service.Replicas != 1 {
		return c.reject(provider.FeatureReplicas, "function concurrency is not a container replica count")
	}
	return nil
}
func (c serviceContract) resources() error {
	limits := functionLimits{}
	if c.service.Resources != nil && (c.service.Resources.CPUMillis != 0 || c.service.Resources.MemoryMiB != 0 && (c.service.Resources.MemoryMiB < limits.MinimumMemory() || c.service.Resources.MemoryMiB > limits.MaximumMemory())) {
		return c.reject(provider.FeatureResources, "declare memory from 128 through 10240 MiB; Lambda derives CPU from memory")
	}
	return nil
}
func (c serviceContract) environment() error {
	var err error
	for index := 0; index < len(c.service.Env) && err == nil; index++ {
		err = c.environmentName(c.service.Env[index].Key)
	}
	return err
}
func (c serviceContract) environmentName(key string) error {
	if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]+$`).MatchString(key) || strings.HasPrefix(key, "AWS_") || strings.HasPrefix(key, "LAMBDA_") || key == "_HANDLER" {
		return c.reject(provider.FeatureFunction, "environment names must be valid user settings, excluding reserved AWS/Lambda names")
	}
	return nil
}
func (c serviceContract) reject(feature provider.Feature, detail string) error {
	return &provider.RejectionError{Provider: (&Provider{}).Name(), Rejections: []provider.Rejection{{Service: c.service.Name, Feature: feature, Detail: detail}}}
}
