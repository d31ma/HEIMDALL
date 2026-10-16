package cloudrun

import (
	"context"
	"fmt"
	"strings"
	"time"

	scheduler "google.golang.org/api/cloudscheduler/v1"
	"google.golang.org/api/option"
	run "google.golang.org/api/run/v2"

	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
)

// A scheduled compose service becomes a Cloud Run job plus a Cloud Scheduler
// job that runs it. Two resources, because Google splits them: Cloud Run owns
// the container, Cloud Scheduler owns the clock. HEIMDALL owns neither, which
// is the point — Cloud Scheduler keeps firing while the control plane is down.

// liveJob is one scheduled service as Google holds it: the run job, and the
// scheduler entry that triggers it.
type liveJob struct {
	job      *run.GoogleCloudRunV2Job
	schedule string
	timezone string
}

func (p *Provider) schedulerClient(ctx context.Context) (*scheduler.Service, error) {
	options := []option.ClientOption{}
	if p.EndpointOverride != "" {
		options = append(options, option.WithEndpoint(p.EndpointOverride))
	}
	if p.NoAuth {
		options = append(options, option.WithoutAuthentication())
	}
	client, err := scheduler.NewService(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("HD0363: build Cloud Scheduler client: %w", err)
	}
	return client, nil
}

// jobsFor reads back the scheduled services of one application, pairing each
// Cloud Run job with the Cloud Scheduler entry that fires it. A job with no
// scheduler entry still reads back: a deleted trigger is drift, and drift the
// operator has to see.
func (p *Provider) jobsFor(
	ctx context.Context, client *run.Service, target provider.Target, app provider.AppRef,
) (map[string]liveJob, error) {
	listed, err := client.Projects.Locations.Jobs.List(parent(target)).Context(ctx).Do()
	if err != nil {
		if isNotFound(err) {
			return map[string]liveJob{}, nil
		}
		return nil, fmt.Errorf("HD0364: list Cloud Run jobs: %w", err)
	}

	out := map[string]liveJob{}
	for _, job := range listed.Jobs {
		annotations := job.Annotations
		if annotations[annotationManagedBy] != managedBy ||
			annotations[annotationProject] != app.Project || annotations[annotationApp] != app.App {
			continue
		}
		name := annotations[annotationService]
		if name == "" {
			continue
		}
		out[name] = liveJob{job: job}
	}
	if len(out) == 0 {
		return out, nil
	}

	schedulerService, err := p.schedulerClient(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := schedulerService.Projects.Locations.Jobs.List(parent(target)).Context(ctx).Do()
	if err != nil && !isNotFound(err) {
		return nil, fmt.Errorf("HD0364: list Cloud Scheduler jobs: %w", err)
	}
	if entries != nil {
		for _, entry := range entries.Jobs {
			service := serviceOfSchedulerJob(entry.Name)
			live, ours := out[service]
			if !ours {
				continue
			}
			live.schedule = entry.Schedule
			live.timezone = entry.TimeZone
			out[service] = live
		}
	}
	return out, nil
}

// serviceOfSchedulerJob reads the service name back out of a scheduler job's
// resource name, which is the only place it can ride: a Cloud Scheduler job
// has no annotations.
func serviceOfSchedulerJob(name string) string {
	id := name[strings.LastIndex(name, "/")+1:]
	parts := strings.SplitN(strings.TrimPrefix(id, "hd-"), "-", 3)
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
}

// jobDefinition builds the Cloud Run job. It shares serviceDefinition's
// container so a job and a service that differ only in scheduling deploy the
// same image, environment, and limits.
func (p *Provider) jobDefinition(
	ctx context.Context, plan provider.Plan, service spec.Service,
) (*run.GoogleCloudRunV2Job, error) {
	definition, err := p.serviceDefinition(ctx, plan, service)
	if err != nil {
		return nil, err
	}
	container := definition.Template.Containers[0]
	// A job's container publishes nothing: there is no request to serve.
	container.Ports = nil

	taskTemplate := &run.GoogleCloudRunV2TaskTemplate{
		Containers: []*run.GoogleCloudRunV2Container{container},
		MaxRetries: int64(service.Schedule.Retries),
	}
	if service.Schedule.TimeoutMS > 0 {
		taskTemplate.Timeout = fmt.Sprintf("%ds", service.Schedule.TimeoutMS/1000)
	}
	return &run.GoogleCloudRunV2Job{
		Annotations: definition.Annotations,
		Template: &run.GoogleCloudRunV2ExecutionTemplate{
			Template: taskTemplate,
		},
	}, nil
}

// applyJob creates or updates the Cloud Run job and its Cloud Scheduler
// trigger. The job comes first: a trigger that fires at a job which does not
// exist yet is an error email at 03:00.
func (p *Provider) applyJob(
	ctx context.Context, client *run.Service, target provider.Target,
	plan provider.Plan, service spec.Service, exists bool,
) error {
	definition, err := p.jobDefinition(ctx, plan, service)
	if err != nil {
		return err
	}
	name := parent(target) + "/jobs/" + resourceID(plan.App, service.Name)
	if exists {
		_, err = client.Projects.Locations.Jobs.Patch(name, definition).Context(ctx).Do()
	} else {
		_, err = client.Projects.Locations.Jobs.Create(parent(target), definition).
			JobId(resourceID(plan.App, service.Name)).Context(ctx).Do()
	}
	if err != nil {
		return fmt.Errorf("HD0375: apply Cloud Run job %s: %w", service.Name, err)
	}
	return p.applySchedulerJob(ctx, target, plan, service, name)
}

// applySchedulerJob registers the clock. A manual-only job — no cron — gets no
// scheduler entry, and an existing one is removed: "run this on request" and
// "run this nightly" must not silently both be true.
func (p *Provider) applySchedulerJob(
	ctx context.Context, target provider.Target, plan provider.Plan, service spec.Service, jobName string,
) error {
	client, err := p.schedulerClient(ctx)
	if err != nil {
		return err
	}
	name := parent(target) + "/jobs/" + resourceID(plan.App, service.Name)

	if service.Schedule.Cron == "" {
		_, err := client.Projects.Locations.Jobs.Delete(name).Context(ctx).Do()
		if err != nil && !isNotFound(err) {
			return fmt.Errorf("HD0376: remove the Cloud Scheduler trigger for %s: %w", service.Name, err)
		}
		return nil
	}

	account := target.Config["scheduler_service_account"]
	if account == "" {
		return fmt.Errorf(
			"HD0377: service %s is scheduled, so target %s needs scheduler_service_account — "+
				"the identity Cloud Scheduler runs the job as",
			service.Name, target.ID)
	}
	entry := &scheduler.Job{
		Name:     name,
		Schedule: service.Schedule.Cron,
		TimeZone: service.Schedule.Timezone,
		HttpTarget: &scheduler.HttpTarget{
			Uri:        "https://run.googleapis.com/v2/" + jobName + ":run",
			HttpMethod: "POST",
			OauthToken: &scheduler.OAuthToken{ServiceAccountEmail: account},
		},
	}
	if service.Schedule.DeadlineMS > 0 {
		entry.AttemptDeadline = fmt.Sprintf("%ds", service.Schedule.DeadlineMS/1000)
	}

	_, err = client.Projects.Locations.Jobs.Patch(name, entry).Context(ctx).Do()
	if isNotFound(err) {
		_, err = client.Projects.Locations.Jobs.Create(parent(target), entry).Context(ctx).Do()
	}
	if err != nil {
		return fmt.Errorf("HD0376: register the Cloud Scheduler trigger for %s: %w", service.Name, err)
	}
	return nil
}

// deleteJob removes the trigger first and the job second, so nothing is ever
// left pointing at something that has gone.
func (p *Provider) deleteJob(
	ctx context.Context, client *run.Service, target provider.Target, app provider.AppRef, service string,
) error {
	name := parent(target) + "/jobs/" + resourceID(app, service)

	schedulerService, err := p.schedulerClient(ctx)
	if err != nil {
		return err
	}
	if _, err := schedulerService.Projects.Locations.Jobs.Delete(name).Context(ctx).Do(); err != nil &&
		!isNotFound(err) {
		return fmt.Errorf("HD0376: delete the Cloud Scheduler trigger for %s: %w", service, err)
	}
	if _, err := client.Projects.Locations.Jobs.Delete(name).Context(ctx).Do(); err != nil && !isNotFound(err) {
		return fmt.Errorf("HD0375: delete Cloud Run job %s: %w", service, err)
	}
	return nil
}

// scheduledState is what Observe reports for a job: the trigger Cloud
// Scheduler actually holds, which is where a console edit shows up.
func scheduledState(live liveJob) provider.ServiceState {
	state := provider.ServiceState{
		Health: provider.Healthy, Schedule: live.schedule,
		Message: "scheduled " + live.schedule + " (" + live.timezone + ")",
	}
	if live.schedule == "" {
		state.Health = provider.Degraded
		state.Message = "the Cloud Scheduler trigger is missing; this job will not run"
	}
	if executed := live.job.LatestCreatedExecution; executed != nil {
		if completed, err := time.Parse(time.RFC3339, executed.CompletionTime); err == nil {
			state.LastRunAt = completed
			state.LastRunStatus = "succeeded"
		}
	}
	return state
}

// RunNow executes the Cloud Run job once, now. The Cloud Scheduler trigger is
// untouched: a manual run is not a change to desired state.
func (p *Provider) RunNow(
	ctx context.Context, target provider.Target, app provider.AppRef, service string,
) error {
	client, err := p.client(ctx)
	if err != nil {
		return err
	}
	name := parent(target) + "/jobs/" + resourceID(app, service)
	_, err = client.Projects.Locations.Jobs.Run(name, &run.GoogleCloudRunV2RunJobRequest{}).Context(ctx).Do()
	if isNotFound(err) {
		return fmt.Errorf("HD0379: %s is not a scheduled service on this target", service)
	}
	if err != nil {
		return fmt.Errorf("HD0379: run %s now: %w", service, err)
	}
	return nil
}
