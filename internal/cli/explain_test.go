package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/reconifyhq/reconify/engine/domain"
	"github.com/reconifyhq/reconify/schemas"
)

func TestExplainCommandPrintsExplanation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	data, err := json.Marshal(domain.Result{Summary: domain.Summary{UnmatchedLeft: 1}, UnmatchedLeft: []domain.Transaction{{ID: "left-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	root := newRootCmd("test", "test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"explain", path, "--top", "1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("explain: %v", err)
	}
	var got schemas.Explanation
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Schema != schemas.ExplanationSchemaV1 || len(got.TopExceptions) != 1 {
		t.Fatalf("unexpected explanation: %+v", got)
	}
}
