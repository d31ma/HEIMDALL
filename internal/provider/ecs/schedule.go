package ecs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	schedulertypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

// A scheduled compose service becomes an EventBridge Scheduler schedule whose
// target is ecs:RunTask against the same task definition an ordinary service
// would run. EventBridge owns the clock: it keeps firing while the control
// plane is down, restarting, or failing over, which is the whole reason
// HEIMDALL declines to own one.

// liveSchedule is one registration as EventBridge holds it.
type liveSchedule struct {
	name string
	// cron is the registration translated back to compose's five-field form,
	// so a schedule edited in the console reads back as what it now is rather
	// than as what HEIMDALL last wrote.
	cron     string
	timezone string
	identity scheduleIdentity
}

// scheduleIdentity rides in the schedule's description. EventBridge tags
// schedule *groups*, not schedules, so the description is the only per-schedule
// place to keep the identity that tags carry everywhere else.
type scheduleIdentity struct {
	ManagedBy   string `json:"managed_by"`
	Project     string `json:"project"`
	App         string `json:"app"`
	Service     string `json:"service"`
	Revision    string `json:"revision"`
	ServiceHash string `json:"service_hash"`
	SpecHash    string `json:"spec_hash"`
}

func (p *Provider) schedulerClient(ctx context.Context, target provider.Target) (*scheduler.Client, error) {
	configuration, err := p.loadConfig(ctx, target)
	if err != nil {
		return nil, err
	}
	endpoint := p.EndpointOverride
	return scheduler.NewFromConfig(configuration, func(o *scheduler.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	}), nil
}

// scheduleName is stable and derivable, because a schedule cannot carry tags:
// the name is how HEIMDALL finds its own registrations again.
func scheduleName(app provider.AppRef, service string) string {
	return fmt.Sprintf("heimdall-%s-%s-%s", app.Project, app.App, service)
}

// schedulesFor lists the registrations HEIMDALL owns for one application.
func (p *Provider) schedulesFor(
	ctx context.Context, client *scheduler.Client, app provider.AppRef,
) (map[string]liveSchedule, error) {
	prefix := fmt.Sprintf("heimdall-%s-%s-", app.Project, app.App)
	listed, err := client.ListSchedules(ctx, &scheduler.ListSchedulesInput{NamePrefix: aws.String(prefix)})
	if err != nil {
		return nil, fmt.Errorf("HD0370: list EventBridge schedules for %s/%s: %w", app.Project, app.App, err)
	}

	out := map[string]liveSchedule{}
	for _, summary := range listed.Schedules {
		name := aws.ToString(summary.Name)
		got, err := client.GetSchedule(ctx, &scheduler.GetScheduleInput{Name: aws.String(name)})
		if err != nil {
			return nil, fmt.Errorf("HD0370: read schedule %s: %w", name, err)
		}
		var identity scheduleIdentity
		if err := json.Unmarshal([]byte(aws.ToString(got.Description)), &identity); err != nil {
			// Someone else's schedule sharing the prefix. Leaving it alone is
			// the only safe answer: HEIMDALL deletes what it created.
			continue
		}
		if identity.ManagedBy != managedBy || identity.Project != app.Project || identity.App != app.App {
			continue
		}
		cron, err := composeCron(aws.ToString(got.ScheduleExpression))
		if err != nil {
			// An expression HEIMDALL cannot express in compose's vocabulary is
			// still drift worth showing, so it is reported verbatim.
			cron = aws.ToString(got.ScheduleExpression)
		}
		out[identity.Service] = liveSchedule{
			name: name, cron: cron,
			timezone: aws.ToString(got.ScheduleExpressionTimezone),
			identity: identity,
		}
	}
	return out, nil
}

// liveSchedules is the registration read every plan and observe makes. It runs
// even for an application that declares no scheduled service, because an
// orphaned rule that nothing declares is exactly what has to be found.
func (p *Provider) liveSchedules(
	ctx context.Context, target provider.Target, app provider.AppRef, _ spec.DeploySpec,
) (map[string]liveSchedule, error) {
	client, err := p.schedulerClient(ctx, target)
	if err != nil {
		return nil, err
	}
	return p.schedulesFor(ctx, client, app)
}

// registerSchedule creates or updates one registration. It is called after the
// task definition is registered, so the schedule always points at a definition
// that exists.
func (p *Provider) registerSchedule(
	ctx context.Context,
	client *scheduler.Client,
	target provider.Target,
	plan provider.Plan,
	service spec.Service,
	taskDefinition string,
	exists bool,
) error {
	expression, err := awsCron(service.Schedule.Cron)
	if err != nil {
		return fmt.Errorf("HD0371: service %s: %w", service.Name, err)
	}
	cluster := clusterARN(target)
	if cluster == "" {
		return fmt.Errorf(
			"HD0372: service %s is scheduled, so target %s needs cluster_arn — EventBridge targets a cluster by ARN, not by name",
			service.Name, target.ID)
	}
	roleARN := target.Config["scheduler_role_arn"]
	if roleARN == "" {
		return fmt.Errorf(
			"HD0372: service %s is scheduled, so target %s needs scheduler_role_arn — the role EventBridge assumes to run the task",
			service.Name, target.ID)
	}

	serviceHash, err := spec.HashService(service)
	if err != nil {
		return err
	}
	description, err := json.Marshal(scheduleIdentity{
		ManagedBy: managedBy, Project: plan.App.Project, App: plan.App.App, Service: service.Name,
		Revision: plan.Revision, ServiceHash: serviceHash, SpecHash: plan.SpecHash,
	})
	if err != nil {
		return err
	}

	ecsParameters := &schedulertypes.EcsParameters{
		TaskDefinitionArn: aws.String(taskDefinition),
		TaskCount:         aws.Int32(1),
		LaunchType:        schedulertypes.LaunchTypeFargate,
	}
	if subnets := strings.Split(target.Config["subnets"], ","); subnets[0] != "" {
		assignPublic := schedulertypes.AssignPublicIpDisabled
		if target.Config["assign_public_ip"] == "true" {
			assignPublic = schedulertypes.AssignPublicIpEnabled
		}
		ecsParameters.NetworkConfiguration = &schedulertypes.NetworkConfiguration{
			AwsvpcConfiguration: &schedulertypes.AwsVpcConfiguration{
				Subnets:        subnets,
				SecurityGroups: nonEmpty(strings.Split(target.Config["security_groups"], ",")),
				AssignPublicIp: assignPublic,
			},
		}
	}

	scheduleTarget := &schedulertypes.Target{
		Arn:           aws.String(cluster),
		RoleArn:       aws.String(roleARN),
		EcsParameters: ecsParameters,
	}
	if service.Schedule.Retries > 0 {
		scheduleTarget.RetryPolicy = &schedulertypes.RetryPolicy{
			MaximumRetryAttempts: aws.Int32(int32(service.Schedule.Retries)),
		}
	}
	// deadline is EventBridge's flexible time window inverted: compose says
	// "a run that cannot start this late is skipped", and OFF is the setting
	// that never starts one late.
	window := &schedulertypes.FlexibleTimeWindow{Mode: schedulertypes.FlexibleTimeWindowModeOff}

	name := scheduleName(plan.App, service.Name)
	timezone := service.Schedule.Timezone
	if timezone == "" {
		timezone = "UTC"
	}
	if exists {
		_, err = client.UpdateSchedule(ctx, &scheduler.UpdateScheduleInput{
			Name: aws.String(name), Description: aws.String(string(description)),
			ScheduleExpression: aws.String(expression), ScheduleExpressionTimezone: aws.String(timezone),
			FlexibleTimeWindow: window, Target: scheduleTarget,
			State: schedulertypes.ScheduleStateEnabled,
		})
	} else {
		_, err = client.CreateSchedule(ctx, &scheduler.CreateScheduleInput{
			Name: aws.String(name), Description: aws.String(string(description)),
			ScheduleExpression: aws.String(expression), ScheduleExpressionTimezone: aws.String(timezone),
			FlexibleTimeWindow: window, Target: scheduleTarget,
			State: schedulertypes.ScheduleStateEnabled,
		})
	}
	if err != nil {
		return fmt.Errorf("HD0373: register schedule for %s: %w", service.Name, err)
	}
	return nil
}

// applySchedule is the create-or-update seam the executor calls.
func (p *Provider) applySchedule(
	ctx context.Context, target provider.Target, plan provider.Plan,
	service spec.Service, taskDefinition string, exists bool,
) error {
	client, err := p.schedulerClient(ctx, target)
	if err != nil {
		return err
	}
	return p.registerSchedule(ctx, client, target, plan, service, taskDefinition, exists)
}

// unregisterSchedule removes the registration for one service, whether or not
// it has one: deleting an absent schedule is a no-op, and checking first would
// only add a race.
func (p *Provider) unregisterSchedule(
	ctx context.Context, target provider.Target, app provider.AppRef, service string,
) error {
	client, err := p.schedulerClient(ctx, target)
	if err != nil {
		return err
	}
	return p.deleteSchedule(ctx, client, scheduleName(app, service))
}

// deleteSchedule removes a registration. It runs *before* the workload is
// deleted: a rule that outlives its task definition fires at 03:00 forever,
// which is the whole footgun this ordering exists to prevent.
func (p *Provider) deleteSchedule(ctx context.Context, client *scheduler.Client, name string) error {
	_, err := client.DeleteSchedule(ctx, &scheduler.DeleteScheduleInput{Name: aws.String(name)})
	if err != nil && !scheduleAbsent(err) {
		return fmt.Errorf("HD0374: delete schedule %s: %w", name, err)
	}
	return nil
}

func scheduleAbsent(err error) bool {
	var notFound *schedulertypes.ResourceNotFoundException
	return errors.As(err, &notFound)
}

// awsCron translates compose's five-field POSIX expression into EventBridge's
// six-field one. Two differences bite: EventBridge wants a year field, and it
// refuses an expression where both day-of-month and day-of-week are set —
// exactly one must be "?". Day-of-week also counts from 1 (Sunday) rather
// than 0, so numeric weekdays shift by one.
func awsCron(expression string) (string, error) {
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return "", fmt.Errorf("%q is not a five-field cron expression", expression)
	}
	minute, hour, dayOfMonth, month, dayOfWeek := fields[0], fields[1], fields[2], fields[3], fields[4]

	shifted, err := shiftWeekdays(dayOfWeek, +1)
	if err != nil {
		return "", err
	}
	dayOfWeek = shifted
	switch {
	case dayOfWeek == "*":
		dayOfWeek = "?"
	case dayOfMonth == "*":
		dayOfMonth = "?"
	default:
		return "", fmt.Errorf(
			"%q sets both a day of month and a day of week; EventBridge Scheduler can honour only one", expression)
	}
	return fmt.Sprintf("cron(%s %s %s %s %s *)", minute, hour, dayOfMonth, month, dayOfWeek), nil
}

// composeCron is awsCron backwards, so live state reads back in the same
// vocabulary the compose file is written in.
func composeCron(expression string) (string, error) {
	body := strings.TrimSuffix(strings.TrimPrefix(expression, "cron("), ")")
	fields := strings.Fields(body)
	if len(fields) != 6 {
		return "", fmt.Errorf("%q is not an EventBridge cron expression", expression)
	}
	dayOfMonth, dayOfWeek := fields[2], fields[4]
	if dayOfMonth == "?" {
		dayOfMonth = "*"
	}
	if dayOfWeek == "?" {
		dayOfWeek = "*"
	} else {
		shifted, err := shiftWeekdays(dayOfWeek, -1)
		if err != nil {
			return "", err
		}
		dayOfWeek = shifted
	}
	return strings.Join([]string{fields[0], fields[1], dayOfMonth, fields[3], dayOfWeek}, " "), nil
}

// shiftWeekdays moves the numeric weekdays in one cron field by delta,
// leaving names, wildcards, and steps alone.
func shiftWeekdays(field string, delta int) (string, error) {
	if field == "*" || field == "?" {
		return field, nil
	}
	var out strings.Builder
	number := strings.Builder{}
	flush := func() error {
		if number.Len() == 0 {
			return nil
		}
		value, err := strconv.Atoi(number.String())
		if err != nil {
			return fmt.Errorf("weekday %q is not a number", number.String())
		}
		// POSIX allows 7 for Sunday as well as 0.
		if value == 7 && delta > 0 {
			value = 0
		}
		out.WriteString(strconv.Itoa(value + delta))
		number.Reset()
		return nil
	}
	for _, r := range field {
		if r >= '0' && r <= '9' {
			number.WriteRune(r)
			continue
		}
		if err := flush(); err != nil {
			return "", err
		}
		out.WriteRune(r)
	}
	if err := flush(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// clusterARN is what EventBridge needs as the schedule's target: an ARN, not
// a cluster name. A target that names only the cluster cannot be scheduled,
// and saying so beats registering a rule AWS will reject at fire time.
func clusterARN(target provider.Target) string {
	return target.Config["cluster_arn"]
}

// RunNow starts one out-of-band execution: the same task the schedule would
// have run, started now. The schedule is left alone — a manual run is not a
// change to desired state.
func (p *Provider) RunNow(
	ctx context.Context, target provider.Target, app provider.AppRef, service string,
) error {
	client, err := p.schedulerClient(ctx, target)
	if err != nil {
		return err
	}
	registered, err := client.GetSchedule(ctx, &scheduler.GetScheduleInput{
		Name: aws.String(scheduleName(app, service)),
	})
	if err != nil {
		if scheduleAbsent(err) {
			return fmt.Errorf("HD0379: %s is not a scheduled service on this target", service)
		}
		return err
	}
	if registered.Target == nil || registered.Target.EcsParameters == nil {
		return fmt.Errorf("HD0379: the schedule for %s does not run an ECS task", service)
	}

	ecsClient, _, _, err := p.clients(ctx, target)
	if err != nil {
		return err
	}
	input := &awsecs.RunTaskInput{
		Cluster:        aws.String(clusterOf(target)),
		TaskDefinition: registered.Target.EcsParameters.TaskDefinitionArn,
		Count:          aws.Int32(1),
		LaunchType:     ecstypes.LaunchTypeFargate,
	}
	if network := registered.Target.EcsParameters.NetworkConfiguration; network != nil &&
		network.AwsvpcConfiguration != nil {
		input.NetworkConfiguration = &ecstypes.NetworkConfiguration{
			AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{
				Subnets:        network.AwsvpcConfiguration.Subnets,
				SecurityGroups: network.AwsvpcConfiguration.SecurityGroups,
				AssignPublicIp: ecstypes.AssignPublicIp(network.AwsvpcConfiguration.AssignPublicIp),
			},
		}
	}
	if _, err := ecsClient.RunTask(ctx, input); err != nil {
		return fmt.Errorf("HD0379: run %s now: %w", service, err)
	}
	return nil
}
