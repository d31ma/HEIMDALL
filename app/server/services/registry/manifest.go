// Package registry implements ADR 0010: the application registry is
// declared in git. One root repository holds a manifest naming projects,
// repositories, targets, groups, and applications; a registry sync diffs
// the manifest against the hd-* collections and closes the gap, with the
// same fail-closed parsing the compose renderer uses — an unmodelled field
// fails by name, never silently.
package registry

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ManifestFile is the file name a root repository declares its registry in.
const ManifestFile = "registry.yaml"

// maxManifestBytes bounds the input, the same posture as compose rendering.
const maxManifestBytes = 1 << 20

// Manifest is the declared registry. Every entry is named; TTIDs never
// appear — the manifest is written by people and reviewed in diffs.
type Manifest struct {
	Projects     []ProjectDecl `yaml:"projects"`
	Repositories []RepoDecl    `yaml:"repositories"`
	Targets      []TargetDecl  `yaml:"targets"`
	Applications []AppDecl     `yaml:"applications"`
}

type ProjectDecl struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

type RepoDecl struct {
	Project          string `yaml:"project"`
	Name             string `yaml:"name"`
	URL              string `yaml:"url"`
	DefaultRef       string `yaml:"default_ref"`
	CredentialRef    string `yaml:"credential_ref"`
	RequireSignature bool   `yaml:"require_signature"`
	WebhookSecretRef string `yaml:"webhook_secret_ref"`
}

type TargetDecl struct {
	Project       string            `yaml:"project"`
	Name          string            `yaml:"name"`
	Provider      string            `yaml:"provider"`
	Region        string            `yaml:"region"`
	Endpoint      string            `yaml:"endpoint"`
	CredentialRef string            `yaml:"credential_ref"`
	Tags          map[string]string `yaml:"tags"`
	Config        map[string]string `yaml:"config"`
}

type AppDecl struct {
	Project    string            `yaml:"project"`
	Name       string            `yaml:"name"`
	Repository string            `yaml:"repository"`
	Target     string            `yaml:"target"`
	Path       string            `yaml:"path"`
	Ref        string            `yaml:"ref"`
	Overlays   []string          `yaml:"overlays"`
	Variables  map[string]string `yaml:"variables"`
	SyncPolicy SyncPolicyDecl    `yaml:"sync_policy"`
	Suspended  bool              `yaml:"suspended"`
}

type SyncPolicyDecl struct {
	Automated bool `yaml:"automated"`
	SelfHeal  bool `yaml:"self_heal"`
	Prune     bool `yaml:"prune"`
}

// Parse decodes and validates a manifest. Decoding runs with KnownFields on,
// so a field this version does not model fails by name — the registry must
// never silently drop a declaration. Validation checks the cross-references
// a diff would otherwise discover halfway through an apply.
func Parse(data []byte) (Manifest, error) {
	var manifest Manifest
	if len(data) > maxManifestBytes {
		return manifest, fmt.Errorf("HD0270: the manifest is %d bytes, over the %d byte limit", len(data), maxManifestBytes)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, fmt.Errorf("HD0270: parse %s: %w", ManifestFile, err)
	}
	if err := manifest.validate(); err != nil {
		return manifest, err
	}
	manifest.normalize()
	return manifest, nil
}

func (m *Manifest) validate() error {
	projects := map[string]bool{}
	for _, project := range m.Projects {
		if project.Name == "" {
			return fmt.Errorf("HD0270: a project needs a name")
		}
		if projects[project.Name] {
			return fmt.Errorf("HD0270: project %q is declared twice", project.Name)
		}
		projects[project.Name] = true
	}

	inProject := func(kind, name, project string) error {
		if name == "" {
			return fmt.Errorf("HD0270: a %s needs a name", kind)
		}
		if project == "" {
			return fmt.Errorf("HD0270: %s %q needs a project", kind, name)
		}
		if !projects[project] {
			return fmt.Errorf("HD0270: %s %q names project %q, which the manifest does not declare",
				kind, name, project)
		}
		return nil
	}

	repos := map[string]bool{}
	for _, repo := range m.Repositories {
		if err := inProject("repository", repo.Name, repo.Project); err != nil {
			return err
		}
		if repo.URL == "" {
			return fmt.Errorf("HD0270: repository %q needs a url", repo.Name)
		}
		if repos[repo.Project+"/"+repo.Name] {
			return fmt.Errorf("HD0270: repository %q is declared twice in project %q", repo.Name, repo.Project)
		}
		repos[repo.Project+"/"+repo.Name] = true
	}

	targets := map[string]bool{}
	for _, target := range m.Targets {
		if err := inProject("target", target.Name, target.Project); err != nil {
			return err
		}
		if target.Provider == "" {
			return fmt.Errorf("HD0270: target %q needs a provider", target.Name)
		}
		if targets[target.Project+"/"+target.Name] {
			return fmt.Errorf("HD0270: target %q is declared twice in project %q", target.Name, target.Project)
		}
		targets[target.Project+"/"+target.Name] = true
	}

	apps := map[string]bool{}
	for _, app := range m.Applications {
		if err := inProject("application", app.Name, app.Project); err != nil {
			return err
		}
		if app.Repository == "" || !repos[app.Project+"/"+app.Repository] {
			return fmt.Errorf(
				"HD0270: application %q names repository %q, which the manifest does not declare in project %q",
				app.Name, app.Repository, app.Project)
		}
		if app.Target == "" || !targets[app.Project+"/"+app.Target] {
			return fmt.Errorf(
				"HD0270: application %q names target %q, which the manifest does not declare in project %q",
				app.Name, app.Target, app.Project)
		}
		if app.Path == "" {
			return fmt.Errorf("HD0270: application %q needs a path", app.Name)
		}
		if apps[app.Project+"/"+app.Name] {
			return fmt.Errorf("HD0270: application %q is declared twice in project %q", app.Name, app.Project)
		}
		apps[app.Project+"/"+app.Name] = true
	}
	return nil
}

// normalize sorts every list so a diff of two manifests is a diff of
// meaning, the same property DeploySpec has.
func (m *Manifest) normalize() {
	sort.Slice(m.Projects, func(a, b int) bool { return m.Projects[a].Name < m.Projects[b].Name })
	key := func(project, name string) string { return project + "/" + name }
	sort.Slice(m.Repositories, func(a, b int) bool {
		return key(m.Repositories[a].Project, m.Repositories[a].Name) < key(m.Repositories[b].Project, m.Repositories[b].Name)
	})
	sort.Slice(m.Targets, func(a, b int) bool {
		return key(m.Targets[a].Project, m.Targets[a].Name) < key(m.Targets[b].Project, m.Targets[b].Name)
	})
	sort.Slice(m.Applications, func(a, b int) bool {
		return key(m.Applications[a].Project, m.Applications[a].Name) < key(m.Applications[b].Project, m.Applications[b].Name)
	})
}
