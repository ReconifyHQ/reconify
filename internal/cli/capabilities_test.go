package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/reconifyhq/reconify/schemas"
)

func TestCapabilitiesCommandDescribesEngineSurface(t *testing.T) {
	root := newRootCmd("test-version", "test-time")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"capabilities"})

	if err := root.Execute(); err != nil {
		t.Fatalf("capabilities: %v", err)
	}

	var got schemas.Capabilities
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if got.Schema != schemas.CapabilitiesSchemaV1 {
		t.Fatalf("schema = %q, want %q", got.Schema, schemas.CapabilitiesSchemaV1)
	}
	if got.Engine.Version != "test-version" {
		t.Fatalf("engine.version = %q, want test-version", got.Engine.Version)
	}
	if got.Commands["reconcile"].Interactive {
		t.Fatalf("reconcile should be non-interactive")
	}
	if !got.Commands["config init"].Interactive {
		t.Fatalf("config init should be marked interactive")
	}
	if got.Formats["reconcile"].Default != "json" {
		t.Fatalf("reconcile default format = %q, want json", got.Formats["reconcile"].Default)
	}
	if len(got.Matching.Passes) != 4 {
		t.Fatalf("matching passes = %d, want 4", len(got.Matching.Passes))
	}
	if got.Schemas["diagnostic"] != schemas.DiagnosticSchemaV1 {
		t.Fatalf("diagnostic schema ID = %q, want %q", got.Schemas["diagnostic"], schemas.DiagnosticSchemaV1)
	}
	if got.Schemas["profile"] != schemas.ProfileSchemaV1 {
		t.Fatalf("profile schema ID = %q, want %q", got.Schemas["profile"], schemas.ProfileSchemaV1)
	}
	if got.Schemas["config_proposal"] != schemas.ConfigProposalSchemaV1 {
		t.Fatalf("config proposal schema ID = %q", got.Schemas["config_proposal"])
	}
	if got.Commands["config infer"].Interactive {
		t.Fatal("config infer should be non-interactive")
	}
	if got.Commands["inspect"].Interactive {
		t.Fatalf("inspect should be non-interactive")
	}
	if got.Formats["inspect"].Default != "json" {
		t.Fatalf("inspect default format = %q, want json", got.Formats["inspect"].Default)
	}
	if got.ErrorCodes["EXCEPTIONS_FOUND"].ExitCode != ErrCodeExceptions {
		t.Fatalf("EXCEPTIONS_FOUND exit code = %d, want %d", got.ErrorCodes["EXCEPTIONS_FOUND"].ExitCode, ErrCodeExceptions)
	}
	if got.ExitCodes["4"] == "" {
		t.Fatal("capabilities omitted exit code 4")
	}
}
