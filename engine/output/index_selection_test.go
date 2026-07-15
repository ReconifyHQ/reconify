//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	. "github.com/reconifyhq/reconify/engine/domain"
)

func testIndexSelection() IndexSelection {
	return IndexSelection{
		RequestedBackend:       "auto",
		Backend:                "disk",
		Reason:                 "memory estimate exceeds configured budget",
		EstimatedMemoryBytes:   128 << 20,
		EstimatedTempDiskBytes: 256 << 20,
		Fallbacks: []IndexFallback{{
			Backend: "memory",
			Reason:  "estimated memory exceeds max_memory_mb=64",
		}},
	}
}

func TestJSONWriter_IndexSelection(t *testing.T) {
	var buf bytes.Buffer
	w := newJSONWriter(&buf)
	if err := w.SetIndexSelection(testIndexSelection()); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteSummary(Summary{}); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	var result Result
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.IndexSelection == nil || result.IndexSelection.Backend != "disk" {
		t.Fatalf("index_selection=%+v, want disk", result.IndexSelection)
	}
}

func TestJSONStreamWriter_IndexSelection(t *testing.T) {
	var buf bytes.Buffer
	w := newJSONStreamWriter(&buf)
	if err := w.SetIndexSelection(testIndexSelection()); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteSummary(Summary{}); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if _, ok := result["index_selection"]; !ok {
		t.Fatal("index_selection field missing")
	}
}

func TestNDJSONWriter_IndexSelectionOrdering(t *testing.T) {
	var buf bytes.Buffer
	w := newNDJSONWriter(&buf)
	if err := w.SetRunInfo(makeRunInfo()); err != nil {
		t.Fatal(err)
	}
	if err := w.SetIndexSelection(testIndexSelection()); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteSummary(Summary{}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines=%d, want 3", len(lines))
	}
	var first, second ndjsonEnvelope
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	if first.Type != "run_info" || second.Type != "index_selection" {
		t.Fatalf("types=%q,%q, want run_info,index_selection", first.Type, second.Type)
	}
}

func TestCSVAndTableWritersOmitIndexSelectionSetter(t *testing.T) {
	if _, ok := any(newCSVWriter(bytes.NewBuffer(nil))).(IndexSelectionSetter); ok {
		t.Fatal("csv writer should not implement IndexSelectionSetter")
	}
	if _, ok := any(newTableWriter(bytes.NewBuffer(nil))).(IndexSelectionSetter); ok {
		t.Fatal("table writer should not implement IndexSelectionSetter")
	}
}
