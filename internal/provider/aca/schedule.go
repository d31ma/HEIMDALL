package aca

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

// A scheduled compose service becomes a Container Apps job with a Schedule
// trigger. ACA owns the clock — it keeps firing while the control plane is
// restarting or failing over, which is the whole reason HEIMDALL declines to
// own one.
//
// One caveat rides in the capability matrix and is enforced here: ACA
// evaluates a job's cron in UTC only. A schedule naming another timezone is
// refused at plan time rather than silently run in the wrong hour.

// isNotFound reports whether Azure said the resource is absent, which for a
// delete means "already gone" rather than "failed".
func isNotFound(err error) bool {
	var responseError *azcore.ResponseError
	return errors.As(err, &responseError) && responseError.StatusCode == http.StatusNotFound
}

func (p *Provider) jobsClient(ctx context.Context, target provider.Target) (*armappcontainers.JobsClient, error) {
	subscription := target.Config["subscription_id"]
	if subscription == "" {
		return nil, fmt.Errorf("HD0375: ACA target %s has no subscription_id configured", target.ID)
	}
	credential, err := p.credential(ctx, target)
	if err != nil {
		return nil, err
	}
	options := &arm.ClientOptions{}
	if p.Transport != nil {
		options.Transport = p.Transport
	}
	client, err := armappcontainers.NewJobsClient(subscription, credential, options)
	if err != nil {
		return nil, fmt.Errorf("HD0375: build ACA jobs client: %w", err)
	}
	return client, nil
}

// rejectNonUTCSchedules is the caveat made real. ACA cron is UTC; converting
// silently would move a backup by an hour twice a year.
func rejectNonUTCSchedules(want spec.DeploySpec) error {
	for _, service := range want.Services {
		if service.Schedule == nil || service.Schedule.Timezone == "" {
			continue
		}
		if !strings.EqualFold(service.Schedule.Timezone, "UTC") {
			return fmt.Errorf(
				"HD0383: service %s schedules in %s, and Container Apps jobs evaluate cron in UTC only; "+
					"state the schedule in UTC, or run this job on a runtime whose scheduler takes a timezone",
				service.Name, service.Schedule.Timezone)
		}
	}
	return nil
}

// jobsFor reads the scheduled services of one application back.
func (p *Provider) jobsFor(
	ctx context.Context, client *armappcontainers.JobsClient, target provider.Target, app provider.AppRef,
) (map[string]*armappcontainers.Job, error) {
	out := map[string]*armappcontainers.Job{}
	pager := client.NewListByResourceGroupPager(resourceGroup(target), nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("HD0378: list Container Apps jobs: %w", err)
		}
		for _, job := range page.Value {
			if job == nil || job.Tags == nil {
				continue
			}
			if value(job.Tags[tagManagedBy]) != managedBy ||
				value(job.Tags[tagProject]) != app.Project || value(job.Tags[tagApp]) != app.App {
				continue
			}
			if name := value(job.Tags[tagService]); name != "" {
				out[name] = job
			}
		}
	}
	return out, nil
}

func value(pointer *string) string {
	if pointer == nil {
		return ""
	}
	return *pointer
}

// jobDefinition builds the Container Apps job. It reuses the app definition's
// container so a job and a service that differ only in scheduling deploy the
// same image, environment, and limits.
func (p *Provider) jobDefinition(
	ctx context.Context, target provider.Target, plan provider.Plan, service spec.Service,
) (armappcontainers.Job, error) {
	app, err := p.appDefinition(ctx, target, plan, service)
	if err != nil {
		return armappcontainers.Job{}, err
	}
	container := app.Properties.Template.Containers[0]

	configuration := &armappcontainers.JobConfiguration{
		TriggerType:       ptr(armappcontainers.TriggerTypeManual),
		ReplicaRetryLimit: ptr(int32(service.Schedule.Retries)),
		ManualTriggerConfig: &armappcontainers.JobConfigurationManualTriggerConfig{
			Parallelism: ptr(int32(1)), ReplicaCompletionCount: ptr(int32(1)),
		},
	}
	if service.Schedule.TimeoutMS > 0 {
		configuration.ReplicaTimeout = ptr(int32(service.Schedule.TimeoutMS / 1000))
	}
	if service.Schedule.Cron != "" {
		configuration.TriggerType = ptr(armappcontainers.TriggerTypeSchedule)
		configuration.ManualTriggerConfig = nil
		configuration.ScheduleTriggerConfig = &armappcontainers.JobConfigurationScheduleTriggerConfig{
			CronExpression: ptr(service.Schedule.Cron),
			Parallelism:    ptr(int32(1)), ReplicaCompletionCount: ptr(int32(1)),
		}
	}

	return armappcontainers.Job{
		Location: app.Location,
		Tags:     app.Tags,
		Properties: &armappcontainers.JobProperties{
			EnvironmentID: ptr(target.Endpoint),
			Configuration: configuration,
			Template: &armappcontainers.JobTemplate{
				Containers: []*armappcontainers.Container{container},
			},
		},
	}, nil
}

// applyJob creates or updates the Container Apps job.
func (p *Provider) applyJob(
	ctx context.Context, target provider.Target, plan provider.Plan, service spec.Service, name string,
) error {
	client, err := p.jobsClient(ctx, target)
	if err != nil {
		return err
	}
	definition, err := p.jobDefinition(ctx, target, plan, service)
	if err != nil {
		return err
	}
	poller, err := client.BeginCreateOrUpdate(ctx, resourceGroup(target), name, definition, nil)
	if err != nil {
		return fmt.Errorf("HD0378: apply Container Apps job %s: %w", service.Name, err)
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

// deleteJob removes the job, and is a no-op when there is none: the delete
// path runs for every service, scheduled or not.
func (p *Provider) deleteJob(ctx context.Context, target provider.Target, name string) error {
	client, err := p.jobsClient(ctx, target)
	if err != nil {
		return err
	}
	poller, err := client.BeginDelete(ctx, resourceGroup(target), name, nil)
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("HD0378: delete Container Apps job %s: %w", name, err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil && !isNotFound(err) {
		return fmt.Errorf("HD0378: delete Container Apps job %s: %w", name, err)
	}
	return nil
}

// scheduledState is what Observe reports for a job: the trigger Azure holds,
// so a cron edited in the portal reads back as drift.
func scheduledState(job *armappcontainers.Job) provider.ServiceState {
	state := provider.ServiceState{Health: provider.Healthy}
	cron := ""
	if job.Properties != nil && job.Properties.Configuration != nil &&
		job.Properties.Configuration.ScheduleTriggerConfig != nil {
		cron = value(job.Properties.Configuration.ScheduleTriggerConfig.CronExpression)
	}
	state.Schedule = cron
	switch cron {
	case "":
		state.Message = "manual-only job; nothing is scheduled"
	default:
		state.Message = "scheduled " + cron + " (UTC)"
	}
	return state
}

// RunNow starts one execution of the Container Apps job. The schedule trigger
// is untouched: a manual run is not a change to desired state.
func (p *Provider) RunNow(
	ctx context.Context, target provider.Target, app provider.AppRef, service string,
) error {
	client, err := p.jobsClient(ctx, target)
	if err != nil {
		return err
	}
	poller, err := client.BeginStart(ctx, resourceGroup(target), resourceName(app, service), nil)
	if err != nil {
		if isNotFound(err) {
			return fmt.Errorf("HD0379: %s is not a scheduled service on this target", service)
		}
		return fmt.Errorf("HD0379: run %s now: %w", service, err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("HD0379: run %s now: %w", service, err)
	}
	return nil
}
