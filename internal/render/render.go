// Package render turns a commit into a DeploySpec, deterministically.
//
// Three properties matter more than the parsing:
//
//   - Nothing is dropped. Decoding runs with KnownFields on, so a Compose
//     directive HEIMDALL does not model fails by name and line. A directive a
//     *provider* cannot express is rejected later, by Capabilities, at plan
//     time — also by name.
//   - No secret value is ever produced. `${secret:...}` is rewritten to a
//     reference and carried as one. Only the apply path resolves it, in
//     process, and only into the provider call.
//   - The same commit renders to the same bytes. Interpolation is single-pass
//     and every collection is sorted, so two machines agree on the content
//     hash.
package render

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/d31ma/heimdall/internal/spec"
	"gopkg.in/yaml.v3"
)

// maxFileBytes bounds one Compose file. A repository is untrusted input; a
// gigabyte of YAML must be a refusal, not an allocation.
const maxFileBytes = 1 << 20

// File is one Compose document, named for error messages.
type File struct {
	Name string
	Data []byte
}

// Input is everything render needs. It is deliberately closed over: no
// environment lookup, no file reads, no network. What is not in here cannot
// influence the output, which is what makes the render reproducible.
type Input struct {
	App      string
	Revision string
	// Files are merged in order: the base first, then each overlay.
	Files []File
	// Variables satisfy ${VAR} interpolation. The host environment is
	// deliberately not consulted; a value the repository does not carry must
	// be supplied explicitly or the render fails.
	Variables map[string]string
}

// Error names exactly where a render failed. Callers show it verbatim: the
// whole point of failing at render time is that the message identifies the
// offending line rather than the operator hunting for it at 3am.
type Error struct {
	Code    string
	File    string
	Service string
	Field   string
	Message string
}

func (e *Error) Error() string {
	location := e.File
	if e.Service != "" {
		location += ": service " + e.Service
	}
	if e.Field != "" {
		location += "." + e.Field
	}
	if location == "" {
		return e.Code + ": " + e.Message
	}
	return e.Code + ": " + location + ": " + e.Message
}

func renderError(code, file, service, field, format string, args ...any) *Error {
	return &Error{Code: code, File: file, Service: service, Field: field,
		Message: fmt.Sprintf(format, args...)}
}

// waveLabel promotes a service into a later sync wave. It is a label rather
// than a Compose extension so that a plain `docker compose up` still works on
// the same file.
const waveLabel = "heimdall.wave"

// metricsLabel selects which metric groups an instance dashboard collects and
// shows for a service, comma-separated. Like the wave, it is a label so a
// plain `docker compose up` of the same file still works.
const metricsLabel = "heimdall.metrics"

// Render produces the canonical desired state for one application at one
// revision.
func Render(in Input) (spec.DeploySpec, error) {
	if in.App == "" {
		return spec.DeploySpec{}, renderError("HD0200", "", "", "", "an application name is required")
	}
	if in.Revision == "" {
		return spec.DeploySpec{}, renderError("HD0200", "", "", "", "a revision is required")
	}
	if len(in.Files) == 0 {
		return spec.DeploySpec{}, renderError("HD0200", "", "", "", "at least one compose file is required")
	}

	merged := map[string]*composeService{}
	secretDefs := map[string]*composeSecret{}
	secretOrigin := map[string]string{}
	for _, file := range in.Files {
		parsed, err := parse(file)
		if err != nil {
			return spec.DeploySpec{}, err
		}
		for name, service := range parsed.Services {
			if existing, ok := merged[name]; ok {
				existing.merge(service)
				continue
			}
			merged[name] = service
		}
		// Overlays override secret declarations wholesale, like scalars.
		for name, secret := range parsed.Secrets {
			secretDefs[name] = secret
			secretOrigin[name] = file.Name
		}
	}
	if len(merged) == 0 {
		return spec.DeploySpec{}, renderError("HD0201", in.Files[0].Name, "", "services",
			"the merged compose files declare no services")
	}

	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)

	// The file a service last appeared in is the best location to blame, and
	// it is what a reader will open. Track it for error messages.
	origin := originOf(in.Files, names)

	services := make([]spec.Service, 0, len(names))
	for _, name := range names {
		converted, err := convert(name, merged[name], in.Variables, origin[name])
		if err != nil {
			return spec.DeploySpec{}, err
		}
		if converted.Secrets, err = convertSecrets(
			merged[name].Secrets, secretDefs, secretOrigin, in.Variables, origin[name], name); err != nil {
			return spec.DeploySpec{}, err
		}
		services = append(services, converted)
	}

	if err := checkDependencies(services, origin); err != nil {
		return spec.DeploySpec{}, err
	}

	rendered := spec.DeploySpec{
		SchemaVersion: spec.SchemaVersion,
		App:           in.App,
		Revision:      in.Revision,
		Services:      services,
	}
	rendered.Normalize()
	return rendered, nil
}

// convertSecrets joins a service's secret list with the top-level
// declarations. Compose's own value sources are refused by name — plaintext
// beside the compose file, the applying machine's environment, and
// undeclared external state are each exactly what GitOps exists to end —
// so the one accepted form is x-heimdall-ref, resolved at apply time.
func convertSecrets(
	mounts []serviceSecret,
	defs map[string]*composeSecret,
	defOrigin map[string]string,
	variables map[string]string,
	file, service string,
) ([]spec.SecretMount, error) {
	if len(mounts) == 0 {
		return nil, nil
	}
	out := make([]spec.SecretMount, 0, len(mounts))
	for _, mount := range mounts {
		def, ok := defs[mount.Source]
		if !ok {
			return nil, renderError("HD0216", file, service, "secrets",
				"secret %q is not declared in the top-level secrets section", mount.Source)
		}
		where := defOrigin[mount.Source]
		switch {
		case def.File != "":
			return nil, renderError("HD0216", where, service, "secrets."+mount.Source,
				"file: would commit the plaintext beside the compose file; use x-heimdall-ref with a ${secret:...} reference")
		case def.Environment != "":
			return nil, renderError("HD0216", where, service, "secrets."+mount.Source,
				"environment: would make the deployed state depend on the machine that applied it; use x-heimdall-ref")
		case def.External:
			return nil, renderError("HD0216", where, service, "secrets."+mount.Source,
				"external: points at state nothing declared; use x-heimdall-ref")
		case def.Ref == "":
			return nil, renderError("HD0216", where, service, "secrets."+mount.Source,
				"declare where the value comes from with x-heimdall-ref: <store>/<name>")
		}
		ref, err := interpolate(def.Ref, variables)
		if err != nil {
			return nil, renderError("HD0206", where, service, "secrets."+mount.Source, "%s", err)
		}
		out = append(out, spec.SecretMount{Name: mount.Source, Ref: ref, Target: mount.Target})
	}
	return out, nil
}

func parse(file File) (*composeFile, error) {
	if len(file.Data) > maxFileBytes {
		return nil, renderError("HD0202", file.Name, "", "",
			"file is %d bytes, over the %d byte limit", len(file.Data), maxFileBytes)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(file.Data))
	// The one line that makes an unmodelled directive fail loudly.
	decoder.KnownFields(true)

	var parsed composeFile
	if err := decoder.Decode(&parsed); err != nil {
		return nil, renderError("HD0203", file.Name, "", "", "%s", err)
	}
	for name := range parsed.Services {
		if !validServiceName(name) {
			return nil, renderError("HD0204", file.Name, name, "",
				"service name must be lowercase alphanumerics, '-', '_', or '.'")
		}
	}
	return &parsed, nil
}

func originOf(files []File, names []string) map[string]string {
	origin := map[string]string{}
	for _, file := range files {
		var parsed composeFile
		// Errors are impossible here: parse already succeeded on every file.
		_ = yaml.Unmarshal(file.Data, &parsed)
		for name := range parsed.Services {
			origin[name] = file.Name
		}
	}
	for _, name := range names {
		if origin[name] == "" && len(files) > 0 {
			origin[name] = files[0].Name
		}
	}
	return origin
}

func convert(name string, service *composeService, variables map[string]string, file string) (spec.Service, error) {
	if service.Image == "" {
		return spec.Service{}, renderError("HD0205", file, name, "image",
			"an image is required; HEIMDALL deploys images, it does not build them")
	}

	image, err := interpolate(service.Image, variables)
	if err != nil {
		return spec.Service{}, renderError("HD0206", file, name, "image", "%s", err)
	}
	if strings.Contains(image, secretPrefix) {
		return spec.Service{}, renderError("HD0207", file, name, "image",
			"an image reference may not be a secret; it is not sensitive and it must be diffable")
	}

	converted := spec.Service{
		Name:      name,
		Image:     image,
		Restart:   service.Restart,
		DependsOn: service.DependsOn.names(),
	}

	if converted.Entrypoint, err = interpolateAll(service.Entrypoint, variables); err != nil {
		return spec.Service{}, renderError("HD0206", file, name, "entrypoint", "%s", err)
	}
	if converted.Command, err = interpolateAll(service.Command, variables); err != nil {
		return spec.Service{}, renderError("HD0206", file, name, "command", "%s", err)
	}

	if converted.Env, err = convertEnvironment(service.Environment, variables, file, name); err != nil {
		return spec.Service{}, err
	}
	if converted.Ports, err = convertPorts(service.Ports, variables, file, name); err != nil {
		return spec.Service{}, err
	}
	if converted.Volumes, err = convertVolumes(service.Volumes, variables, file, name); err != nil {
		return spec.Service{}, err
	}
	if converted.Labels, converted.Wave, converted.Metrics, err = convertLabels(service.Labels, variables, file, name); err != nil {
		return spec.Service{}, err
	}
	if converted.Healthcheck, err = convertHealthcheck(service.Healthcheck, file, name); err != nil {
		return spec.Service{}, err
	}
	if converted.Replicas, converted.Resources, err = convertDeploy(service.Deploy, file, name); err != nil {
		return spec.Service{}, err
	}

	if service.Restart != "" && !validRestart(service.Restart) {
		return spec.Service{}, renderError("HD0208", file, name, "restart",
			"%q is not one of no, always, on-failure, unless-stopped", service.Restart)
	}
	return converted, nil
}

func convertEnvironment(environment keyValues, variables map[string]string, file, service string) ([]spec.EnvVar, error) {
	if len(environment) == 0 {
		return nil, nil
	}
	keys := sortedKeys(environment)
	out := make([]spec.EnvVar, 0, len(keys))
	for _, key := range keys {
		if !validEnvKey(key) {
			return nil, renderError("HD0209", file, service, "environment."+key,
				"environment keys must match [A-Za-z_][A-Za-z0-9_]*")
		}
		reference, isSecret, err := secretReference(environment[key])
		if err != nil {
			return nil, renderError("HD0210", file, service, "environment."+key, "%s", err)
		}
		if isSecret {
			// The reference is carried, never resolved. Nothing downstream of
			// here can leak a value it was never given.
			out = append(out, spec.EnvVar{Key: key, Ref: reference})
			continue
		}
		value, err := interpolate(environment[key], variables)
		if err != nil {
			return nil, renderError("HD0206", file, service, "environment."+key, "%s", err)
		}
		out = append(out, spec.EnvVar{Key: key, Value: value})
	}
	return out, nil
}

var portPattern = regexp.MustCompile(`^(?:(\d{1,5}):)?(\d{1,5})(?:/(tcp|udp))?$`)

func convertPorts(ports []string, variables map[string]string, file, service string) ([]spec.Port, error) {
	if len(ports) == 0 {
		return nil, nil
	}
	out := make([]spec.Port, 0, len(ports))
	for _, raw := range ports {
		expanded, err := interpolate(raw, variables)
		if err != nil {
			return nil, renderError("HD0206", file, service, "ports", "%s", err)
		}
		match := portPattern.FindStringSubmatch(expanded)
		if match == nil {
			return nil, renderError("HD0211", file, service, "ports",
				"%q is not HOST:CONTAINER[/tcp|udp]; host ranges and host-IP bindings are not modelled", expanded)
		}
		target, err := strconv.Atoi(match[2])
		if err != nil || target < 1 || target > 65535 {
			return nil, renderError("HD0211", file, service, "ports", "container port %q is out of range", match[2])
		}
		published := target
		if match[1] != "" {
			if published, err = strconv.Atoi(match[1]); err != nil || published < 1 || published > 65535 {
				return nil, renderError("HD0211", file, service, "ports", "host port %q is out of range", match[1])
			}
		}
		protocol := match[3]
		if protocol == "" {
			protocol = "tcp"
		}
		out = append(out, spec.Port{Published: published, Target: target, Protocol: protocol})
	}
	return out, nil
}

func convertVolumes(volumes []string, variables map[string]string, file, service string) ([]spec.Volume, error) {
	if len(volumes) == 0 {
		return nil, nil
	}
	out := make([]spec.Volume, 0, len(volumes))
	for _, raw := range volumes {
		expanded, err := interpolate(raw, variables)
		if err != nil {
			return nil, renderError("HD0206", file, service, "volumes", "%s", err)
		}
		parts := strings.Split(expanded, ":")
		if len(parts) < 2 || len(parts) > 3 {
			return nil, renderError("HD0212", file, service, "volumes",
				"%q is not SOURCE:TARGET[:ro]; anonymous volumes are not modelled", expanded)
		}
		if !strings.HasPrefix(parts[1], "/") {
			return nil, renderError("HD0212", file, service, "volumes",
				"%q has a relative container path; the target must be absolute", expanded)
		}
		volume := spec.Volume{Source: parts[0], Target: parts[1]}
		if len(parts) == 3 {
			switch parts[2] {
			case "ro":
				volume.ReadOnly = true
			case "rw":
			default:
				return nil, renderError("HD0212", file, service, "volumes",
					"%q has mode %q; only ro and rw are modelled", expanded, parts[2])
			}
		}
		out = append(out, volume)
	}
	return out, nil
}

func convertLabels(labels keyValues, variables map[string]string, file, service string) ([]spec.Label, int, []string, error) {
	if len(labels) == 0 {
		return nil, 0, nil, nil
	}
	wave := 0
	var metrics []string
	keys := sortedKeys(labels)
	out := make([]spec.Label, 0, len(keys))
	for _, key := range keys {
		value, err := interpolate(labels[key], variables)
		if err != nil {
			return nil, 0, nil, renderError("HD0206", file, service, "labels."+key, "%s", err)
		}
		if key == waveLabel {
			if wave, err = strconv.Atoi(value); err != nil {
				return nil, 0, nil, renderError("HD0213", file, service, "labels."+waveLabel,
					"%q is not an integer", value)
			}
			// The wave is promoted onto the service and dropped from labels,
			// so it cannot drift from the field the reconciler actually reads.
			continue
		}
		if key == metricsLabel {
			if metrics, err = parseMetrics(value); err != nil {
				return nil, 0, nil, renderError("HD0214", file, service, "labels."+metricsLabel, "%s", err)
			}
			continue
		}
		out = append(out, spec.Label{Key: key, Value: value})
	}
	if len(out) == 0 {
		return nil, wave, metrics, nil
	}
	return out, wave, metrics, nil
}

// parseMetrics validates a comma-separated selection against the closed
// vocabulary. A typo is rejected with the allowed names, never silently
// collected as nothing.
func parseMetrics(value string) ([]string, error) {
	allowed := make([]string, 0, len(spec.MetricGroups))
	for name := range spec.MetricGroups {
		allowed = append(allowed, name)
	}
	sort.Strings(allowed)

	var out []string
	for _, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !spec.MetricGroups[name] {
			return nil, fmt.Errorf("%q is not a metric group; the choices are %s",
				name, strings.Join(allowed, ", "))
		}
		out = append(out, name)
	}
	return out, nil
}

func convertHealthcheck(check *healthcheck, file, service string) (*spec.Healthcheck, error) {
	if check == nil {
		return nil, nil
	}
	if len(check.Test) == 0 {
		return nil, renderError("HD0214", file, service, "healthcheck.test", "a test is required")
	}
	converted := &spec.Healthcheck{Test: check.Test, Retries: check.Retries}

	for _, field := range []struct {
		name  string
		value string
		into  *int
	}{
		{"interval", check.Interval, &converted.IntervalMS},
		{"timeout", check.Timeout, &converted.TimeoutMS},
		{"start_period", check.StartPeriod, &converted.StartPerdMS},
	} {
		if field.value == "" {
			continue
		}
		duration, err := time.ParseDuration(field.value)
		if err != nil || duration <= 0 {
			return nil, renderError("HD0215", file, service, "healthcheck."+field.name,
				"%q is not a positive duration such as 10s or 1m30s", field.value)
		}
		*field.into = int(duration.Milliseconds())
	}
	return converted, nil
}

func convertDeploy(settings *deploy, file, service string) (int, *spec.Resources, error) {
	if settings == nil {
		return 0, nil, nil
	}
	replicas := 0
	if settings.Replicas != nil {
		if *settings.Replicas < 0 {
			return 0, nil, renderError("HD0216", file, service, "deploy.replicas", "must not be negative")
		}
		replicas = *settings.Replicas
	}
	if settings.Resources == nil || settings.Resources.Limits == nil {
		return replicas, nil, nil
	}

	limits := settings.Resources.Limits
	converted := &spec.Resources{}
	if limits.CPUs != "" {
		cpus, err := strconv.ParseFloat(limits.CPUs, 64)
		if err != nil || cpus <= 0 {
			return 0, nil, renderError("HD0217", file, service, "deploy.resources.limits.cpus",
				"%q is not a positive number of CPUs", limits.CPUs)
		}
		converted.CPUMillis = int(cpus * 1000)
	}
	if limits.Memory != "" {
		bytes, err := parseMemory(limits.Memory)
		if err != nil {
			return 0, nil, renderError("HD0218", file, service, "deploy.resources.limits.memory", "%s", err)
		}
		converted.MemoryMiB = int(bytes / (1 << 20))
	}
	return replicas, converted, nil
}

// checkDependencies rejects a depends_on naming a service the merged files do
// not define, and any cycle. Both would otherwise surface as a reconcile that
// never completes, which is a much worse place to learn about it.
func checkDependencies(services []spec.Service, origin map[string]string) error {
	known := make(map[string]spec.Service, len(services))
	for _, service := range services {
		known[service.Name] = service
	}
	for _, service := range services {
		for _, dependency := range service.DependsOn {
			if _, ok := known[dependency]; !ok {
				return renderError("HD0219", origin[service.Name], service.Name, "depends_on",
					"depends on %q, which no compose file defines", dependency)
			}
		}
	}

	// Iterative depth-first search with an explicit stack: the graph comes
	// from untrusted input and recursion here would be a stack-depth attack.
	const (
		unvisited = 0
		active    = 1
		done      = 2
	)
	state := make(map[string]int, len(services))
	var cycle []string

	for _, root := range services {
		if state[root.Name] != unvisited {
			continue
		}
		type frame struct {
			name  string
			index int
		}
		stack := []frame{{name: root.Name}}
		state[root.Name] = active
		path := []string{root.Name}

		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			dependencies := known[top.name].DependsOn
			if top.index >= len(dependencies) {
				state[top.name] = done
				stack = stack[:len(stack)-1]
				path = path[:len(path)-1]
				continue
			}
			next := dependencies[top.index]
			top.index++

			switch state[next] {
			case active:
				cycle = append(append([]string{}, path...), next)
			case unvisited:
				state[next] = active
				stack = append(stack, frame{name: next})
				path = append(path, next)
			}
			if cycle != nil {
				return renderError("HD0220", origin[cycle[0]], cycle[0], "depends_on",
					"dependency cycle: %s", strings.Join(cycle, " -> "))
			}
		}
	}
	return nil
}

func sortedKeys(values keyValues) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

var (
	servicePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	envKeyPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func validServiceName(name string) bool { return servicePattern.MatchString(name) }
func validEnvKey(key string) bool       { return envKeyPattern.MatchString(key) }

func validRestart(policy string) bool {
	switch policy {
	case "no", "always", "on-failure", "unless-stopped":
		return true
	}
	return false
}

// parseMemory reads Compose's byte suffixes. Values are binary multiples,
// matching Compose and Docker rather than SI.
func parseMemory(value string) (int64, error) {
	trimmed := strings.TrimSpace(value)
	multiplier := int64(1)
	switch {
	case strings.HasSuffix(trimmed, "G"), strings.HasSuffix(trimmed, "g"):
		multiplier, trimmed = 1<<30, trimmed[:len(trimmed)-1]
	case strings.HasSuffix(trimmed, "M"), strings.HasSuffix(trimmed, "m"):
		multiplier, trimmed = 1<<20, trimmed[:len(trimmed)-1]
	case strings.HasSuffix(trimmed, "K"), strings.HasSuffix(trimmed, "k"):
		multiplier, trimmed = 1<<10, trimmed[:len(trimmed)-1]
	}
	amount, err := strconv.ParseInt(strings.TrimSuffix(trimmed, "b"), 10, 64)
	if err != nil || amount <= 0 {
		return 0, fmt.Errorf("%q is not a positive byte size such as 512M or 2G", value)
	}
	return amount * multiplier, nil
}
