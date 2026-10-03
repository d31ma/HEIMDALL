package lambda

import (
	"regexp"
	"sort"
	"strings"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

type RuntimeContract struct {
	Target provider.Target
	Spec   spec.DeploySpec
}

func (c RuntimeContract) Validate() error {
	return c.validate()
}

func (c RuntimeContract) validate() error {
	if err := provider.Validate((&Provider{}).Capabilities(), c.Spec); err != nil {
		return err
	}
	for _, service := range c.Spec.Services {
		if service.Function == nil {
			return c.reject(service, provider.FeatureFunction, "declare lambda-runtime-api with direct json invocation")
		}
	}
	if c.Target.Region == "" {
		return (Diagnostic{}).Format("HD0410: Lambda target requires a region")
	}
	if !regexp.MustCompile(`^arn:aws(?:-us-gov|-cn)?:iam::[0-9]{12}:role/[A-Za-z0-9+=,.@_/-]+$`).MatchString(c.Target.Config["execution_role_arn"]) {
		return (Diagnostic{}).Format("HD0410: Lambda target requires an existing execution_role_arn")
	}
	keys := []string{}
	for key := range c.Target.Config {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if key != "execution_role_arn" {
			return (Diagnostic{}).Format("HD0410: Lambda target setting %s is not implemented; network, concurrency and event-source changes need explicit contracts", key)
		}
	}
	for _, service := range c.Spec.Services {
		if err := c.serviceError(service); err != nil {
			return err
		}
	}
	return nil
}

func (c RuntimeContract) serviceError(s spec.Service) error {
	if s.Function == nil || s.Function.RuntimeContract != "lambda-runtime-api" || s.Function.Invocation != "json" {
		return c.reject(s, provider.FeatureFunction, "declare lambda-runtime-api with direct json invocation")
	}
	if err := s.Function.Validate(); err != nil {
		return err
	}
	image := regexp.MustCompile(`^[0-9]{12}\.dkr\.ecr\.` + regexp.QuoteMeta(c.Target.Region) + `\.amazonaws\.com(?:\.cn)?/[a-z0-9._/-]+@sha256:[a-f0-9]{64}$`)
	if !image.MatchString(s.Image) {
		return c.reject(s, provider.FeatureFunction, "use an immutable same-region ECR image digest implementing the Lambda Runtime API")
	}
	if s.Update != nil {
		return c.reject(s, provider.FeatureStopFirst, "container rollout directives do not describe Lambda version publication")
	}
	if s.Replicas != 0 && s.Replicas != 1 {
		return c.reject(s, provider.FeatureReplicas, "function concurrency is not a container replica count")
	}
	if s.Resources != nil && (s.Resources.CPUMillis != 0 || s.Resources.MemoryMiB != 0 && (s.Resources.MemoryMiB < 128 || s.Resources.MemoryMiB > 10240)) {
		return c.reject(s, provider.FeatureResources, "declare memory from 128 through 10240 MiB; Lambda derives CPU from memory")
	}
	for _, env := range s.Env {
		if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]+$`).MatchString(env.Key) || strings.HasPrefix(env.Key, "AWS_") || strings.HasPrefix(env.Key, "LAMBDA_") || env.Key == "_HANDLER" {
			return c.reject(s, provider.FeatureFunction, "environment names must be valid user settings, excluding reserved AWS/Lambda names")
		}
	}
	return nil
}

func (c RuntimeContract) reject(s spec.Service, f provider.Feature, detail string) error {
	return &provider.RejectionError{Provider: "lambda", Rejections: []provider.Rejection{{Service: s.Name, Feature: f, Detail: detail}}}
}
