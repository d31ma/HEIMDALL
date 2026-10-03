// Package schema validates JSON against the CHEX schemas in schemas/.
//
// CHEX rejects any property the schema does not name. That strictness is the
// feature: a compose directive HEIMDALL does not model fails validation by
// name instead of being silently dropped, which is the standing mitigation
// for the lossy-translation risk.
package schema

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/d31ma/heimdall/internal/schema/chex"
)

// Names of the shipped schemas, resolved against Dir.
const (
	DeploySpec     = "deployspec"
	ComposeService = "compose-service"
)

// Dir returns the absolute path of the schemas directory. It is resolved from
// this file's own location so tests and the binary agree without a flag.
func Dir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "schemas"
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "schemas")
}

// Validator drives one long-lived chex process. It is safe for concurrent
// use; the underlying client serializes frames.
type Validator struct {
	once   sync.Once
	client *chex.CHEX
	err    error
	dir    string
}

// New returns a validator over the shipped schemas. The chex binary is taken
// from CHEX_BINARY or PATH.
func New() *Validator { return &Validator{dir: Dir()} }

// NewIn returns a validator over an explicit schema directory, for tests that
// exercise a fixture schema.
func NewIn(dir string) *Validator { return &Validator{dir: dir} }

func (v *Validator) start() (*chex.CHEX, error) {
	v.once.Do(func() {
		binary := os.Getenv("CHEX_BINARY")
		if binary == "" {
			binary = "chex"
		}
		v.client, v.err = chex.Open(binary)
	})
	return v.client, v.err
}

// Validate checks data against the named schema. A validation failure and a
// transport failure are both errors, and neither is ever treated as a pass:
// there is no path through this function that approves unvalidated input.
func (v *Validator) Validate(name string, data map[string]any) error {
	client, err := v.start()
	if err != nil {
		return fmt.Errorf("HD0020: start chex: %w", err)
	}
	path := filepath.Join(v.dir, name+".schema.json")
	if _, err := client.Validate(path, data, ""); err != nil {
		return fmt.Errorf("HD0021: %s: %w", name, err)
	}
	return nil
}

// Close stops the chex process.
func (v *Validator) Close() error {
	if v.client == nil {
		return nil
	}
	return v.client.Close()
}
