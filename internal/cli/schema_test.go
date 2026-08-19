package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/reconifyhq/reconify/schemas"
)

func TestResultSchemaCommandPrintsPublishedArtifact(t *testing.T) {
	root := newRootCmd("test", "test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"schema", "result"})

	if err := root.Execute(); err != nil {
		t.Fatalf("schema result: %v", err)
	}
	if !bytes.Equal(out.Bytes(), schemas.ResultV1()) {
		t.Fatal("schema result output differs from embedded published artifact")
	}
}

func TestConfigSchemaIncludesPublishedResultSchema(t *testing.T) {
	root := newRootCmd("test", "test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"config", "schema"})

	if err := root.Execute(); err != nil {
		t.Fatalf("config schema: %v", err)
	}
	var payload struct {
		ResultSchemaID string          `json:"result_schema_id"`
		ResultSchema   json.RawMessage `json:"result_schema"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode config schema output: %v", err)
	}
	if payload.ResultSchemaID != schemas.ResultSchemaV1 {
		t.Fatalf("result_schema_id = %q, want %q", payload.ResultSchemaID, schemas.ResultSchemaV1)
	}
	if !json.Valid(payload.ResultSchema) {
		t.Fatal("result_schema is not valid JSON")
	}
	if !bytes.Equal(payload.ResultSchema, schemas.ResultV1()) {
		var got, want any
		if err := json.Unmarshal(payload.ResultSchema, &got); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(schemas.ResultV1(), &want); err != nil {
			t.Fatal(err)
		}
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		if !bytes.Equal(gotJSON, wantJSON) {
			t.Fatal("config schema result_schema differs from published artifact")
		}
	}
}
