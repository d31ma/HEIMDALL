package render

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// composeFile is the subset of Compose HEIMDALL models. Decoding runs with
// KnownFields on, so a directive that is not listed here fails by name and
// line rather than being silently dropped — the standing mitigation for lossy
// translation. Widening the subset is a deliberate edit here, in the CHEX
// schema, and in every adapter's Capabilities.
type composeFile struct {
	Name     string                     `yaml:"name"`
	Services map[string]*composeService `yaml:"services"`
	// Top-level volumes are accepted and ignored: a named volume is declared
	// here and used in a service, and rejecting the declaration would reject
	// every real multi-service file. What the volume *means* per provider is
	// Capabilities' problem, not the parser's.
	Volumes map[string]yaml.Node `yaml:"volumes"`
	// Top-level secrets declare where each file secret's value comes from.
	// Only the x-heimdall-ref form is accepted — see composeSecret.
	Secrets map[string]*composeSecret `yaml:"secrets"`
}

// composeSecret is one top-level secret declaration. Compose's own forms
// are parsed so they can be *refused by name*: `file:` would commit
// plaintext beside the compose file, `environment:` would make the deployed
// state depend on the machine that applied it, and `external:` points at
// state nothing declared. What GitOps can honour is a reference resolved at
// apply time, which is what x-heimdall-ref is.
type composeSecret struct {
	File        string `yaml:"file"`
	Environment string `yaml:"environment"`
	External    bool   `yaml:"external"`
	Name        string `yaml:"name"`
	Ref         string `yaml:"x-heimdall-ref"`
}

type composeService struct {
	Image         string          `yaml:"image"`
	ContainerName string          `yaml:"container_name"`
	Entrypoint    stringList      `yaml:"entrypoint"`
	Command       stringList      `yaml:"command"`
	Environment   keyValues       `yaml:"environment"`
	Ports         []string        `yaml:"ports"`
	Volumes       []string        `yaml:"volumes"`
	DependsOn     dependencies    `yaml:"depends_on"`
	Restart       string          `yaml:"restart"`
	Healthcheck   *healthcheck    `yaml:"healthcheck"`
	Labels        keyValues       `yaml:"labels"`
	Deploy        *deploy         `yaml:"deploy"`
	Secrets       []serviceSecret `yaml:"secrets"`
}

// serviceSecret accepts Compose's short form (a bare secret name) and the
// long form's source/target. Other long-form fields (uid, gid, mode) are
// refused by name rather than silently dropped.
type serviceSecret struct {
	Source string
	Target string
}

func (s *serviceSecret) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var name string
		if err := node.Decode(&name); err != nil {
			return err
		}
		s.Source = name
		return nil
	case yaml.MappingNode:
		var raw map[string]yaml.Node
		if err := node.Decode(&raw); err != nil {
			return err
		}
		for key, value := range raw {
			switch key {
			case "source":
				s.Source = value.Value
			case "target":
				s.Target = value.Value
			default:
				return fmt.Errorf("line %d: secret field %q is not modelled; only source and target are", node.Line, key)
			}
		}
		if s.Source == "" {
			return fmt.Errorf("line %d: a service secret needs a source", node.Line)
		}
		return nil
	default:
		return fmt.Errorf("line %d: expected a secret name or a source/target mapping", node.Line)
	}
}

type healthcheck struct {
	Test        stringList `yaml:"test"`
	Interval    string     `yaml:"interval"`
	Timeout     string     `yaml:"timeout"`
	StartPeriod string     `yaml:"start_period"`
	Retries     int        `yaml:"retries"`
}

type deploy struct {
	Replicas  *int       `yaml:"replicas"`
	Resources *resources `yaml:"resources"`
}

type resources struct {
	Limits *resourceLimits `yaml:"limits"`
}

type resourceLimits struct {
	CPUs   string `yaml:"cpus"`
	Memory string `yaml:"memory"`
}

// stringList accepts Compose's "either a string or a list" shape. A bare
// string is *not* split on whitespace: `command: echo "a b"` is one argument
// in the shell form, and guessing at word splitting is how a deploy silently
// runs the wrong thing. It becomes a single-element argv, which is what a
// shell-form command means once there is no shell.
type stringList []string

func (s *stringList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var single string
		if err := node.Decode(&single); err != nil {
			return err
		}
		*s = stringList{single}
		return nil
	case yaml.SequenceNode:
		var many []string
		if err := node.Decode(&many); err != nil {
			return err
		}
		*s = many
		return nil
	default:
		return fmt.Errorf("line %d: expected a string or a list of strings", node.Line)
	}
}

// keyValues accepts Compose's map form and its KEY=VALUE list form, and
// normalises both to a map. A list entry with no `=` is a pass-through of the
// host environment, which GitOps must refuse: the deployed state would then
// depend on the machine that applied it.
type keyValues map[string]string

func (k *keyValues) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.MappingNode:
		// Decode into a node map first so numbers and booleans survive as the
		// scalars they were written as, rather than becoming Go zero values.
		var raw map[string]yaml.Node
		if err := node.Decode(&raw); err != nil {
			return err
		}
		out := make(keyValues, len(raw))
		for key, value := range raw {
			out[key] = value.Value
		}
		*k = out
		return nil
	case yaml.SequenceNode:
		var entries []string
		if err := node.Decode(&entries); err != nil {
			return err
		}
		out := make(keyValues, len(entries))
		for _, entry := range entries {
			key, value, found := strings.Cut(entry, "=")
			if !found {
				return fmt.Errorf(
					"line %d: %q inherits from the host environment; GitOps requires an explicit value or a ${secret:...} reference",
					node.Line, entry)
			}
			out[key] = value
		}
		*k = out
		return nil
	default:
		return fmt.Errorf("line %d: expected a mapping or a list of KEY=VALUE entries", node.Line)
	}
}

// dependencies accepts the list form and the long `condition:` form. The
// condition itself is kept so Capabilities can reject what a provider cannot
// express, rather than the parser quietly flattening it away.
type dependencies map[string]string

func (d *dependencies) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.SequenceNode:
		var names []string
		if err := node.Decode(&names); err != nil {
			return err
		}
		out := make(dependencies, len(names))
		for _, name := range names {
			out[name] = "service_started"
		}
		*d = out
		return nil
	case yaml.MappingNode:
		var long map[string]struct {
			Condition string `yaml:"condition"`
		}
		if err := node.Decode(&long); err != nil {
			return err
		}
		out := make(dependencies, len(long))
		for name, body := range long {
			condition := body.Condition
			if condition == "" {
				condition = "service_started"
			}
			out[name] = condition
		}
		*d = out
		return nil
	default:
		return fmt.Errorf("line %d: expected a list of service names or a mapping of conditions", node.Line)
	}
}

func (d dependencies) names() []string {
	names := make([]string, 0, len(d))
	for name := range d {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// merge applies an overlay over the base, following Compose's own override
// rules: scalars are replaced, mappings are merged key by key, and the
// multi-value sequences are concatenated. `command` and `entrypoint` are
// replaced wholesale — appending to an argv would produce a command line
// nobody wrote.
func (base *composeService) merge(overlay *composeService) {
	if overlay.Image != "" {
		base.Image = overlay.Image
	}
	if overlay.ContainerName != "" {
		base.ContainerName = overlay.ContainerName
	}
	if overlay.Restart != "" {
		base.Restart = overlay.Restart
	}
	if overlay.Entrypoint != nil {
		base.Entrypoint = overlay.Entrypoint
	}
	if overlay.Command != nil {
		base.Command = overlay.Command
	}
	if overlay.Healthcheck != nil {
		base.Healthcheck = overlay.Healthcheck
	}

	base.Environment = mergeKeyValues(base.Environment, overlay.Environment)
	base.Labels = mergeKeyValues(base.Labels, overlay.Labels)
	base.Ports = append(append([]string{}, base.Ports...), overlay.Ports...)
	base.Volumes = append(append([]string{}, base.Volumes...), overlay.Volumes...)
	base.Secrets = append(append([]serviceSecret{}, base.Secrets...), overlay.Secrets...)

	if overlay.DependsOn != nil {
		if base.DependsOn == nil {
			base.DependsOn = dependencies{}
		}
		for name, condition := range overlay.DependsOn {
			base.DependsOn[name] = condition
		}
	}

	if overlay.Deploy != nil {
		if base.Deploy == nil {
			base.Deploy = &deploy{}
		}
		if overlay.Deploy.Replicas != nil {
			base.Deploy.Replicas = overlay.Deploy.Replicas
		}
		if overlay.Deploy.Resources != nil {
			base.Deploy.Resources = overlay.Deploy.Resources
		}
	}
}

func mergeKeyValues(base, overlay keyValues) keyValues {
	if base == nil && overlay == nil {
		return nil
	}
	merged := make(keyValues, len(base)+len(overlay))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range overlay {
		merged[key] = value
	}
	return merged
}
