// Package chex drives the `chex` binary's persistent NDJSON loop.
//
// Stdlib only. Requires the `chex` binary on PATH or an explicit path. One
// long-lived subprocess.
//
//	c, _ := chex.Open("chex")
//	defer c.Close()
//	data, _ := c.Validate("./schemas/person.schema.json", map[string]any{"name": "Ada"}, "")
//	data, _ := c.Validate("person", map[string]any{"name": "Ada"}, "./schemas")
//
// Validate returns the validated data (or an error when it does not match the
// schema). Method names use Go's PascalCase. Request(op) is a raw escape hatch
// returning the full response.
package chex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

type CHEX struct {
	cmd  *exec.Cmd
	pipe io.WriteCloser
	in   *bufio.Writer
	out  *bufio.Reader
	mu   sync.Mutex
}

// Open starts a warm chex process. binary defaults to "chex".
func Open(binary string) (*CHEX, error) {
	if binary == "" {
		binary = "chex"
	}
	cmd := exec.Command(binary, "exec", "--loop")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &CHEX{cmd: cmd, pipe: stdin, in: bufio.NewWriter(stdin), out: bufio.NewReader(stdout)}, nil
}

// Request sends one raw machine-protocol op and returns the full response.
func (c *CHEX) Request(op map[string]any) (map[string]any, error) {
	c.mu.Lock() // ponytail: one call in flight; drop the lock only if you pipeline
	defer c.mu.Unlock()
	line, err := json.Marshal(op)
	if err != nil {
		return nil, err
	}
	if _, err := c.in.Write(append(line, '\n')); err != nil {
		return nil, err
	}
	if err := c.in.Flush(); err != nil {
		return nil, err
	}
	reply, err := c.out.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("chex closed the stream: %w", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(reply, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *CHEX) op(name string, fields map[string]any) (any, error) {
	payload := map[string]any{"op": name}
	for k, v := range fields {
		if v != nil {
			payload[k] = v
		}
	}
	resp, err := c.Request(payload)
	if err != nil {
		return nil, err
	}
	if ok, _ := resp["ok"].(bool); !ok {
		msg := "chex error"
		if e, ok := resp["error"].(map[string]any); ok {
			if m, ok := e["message"].(string); ok {
				msg = m
			}
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return resp["result"], nil
}

// Validate checks data against a schema (name or .schema.json path). Pass "" for
// schemaDir when giving a full path. Returns the validated data.
func (c *CHEX) Validate(schema string, data map[string]any, schemaDir string) (any, error) {
	fields := map[string]any{"schema": schema, "data": data}
	if schemaDir != "" {
		fields["schemaDir"] = schemaDir
	}
	return c.op("validate", fields)
}

// Close ends the loop and waits for the process to exit.
func (c *CHEX) Close() error {
	c.in.Flush()
	c.pipe.Close()
	return c.cmd.Wait()
}
