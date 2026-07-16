//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package reconcile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reconifyhq/reconify/config"
	. "github.com/reconifyhq/reconify/engine/domain"
)

func TestReconcilePartitionedMultiSourceMatchesStreamingMultiSource(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	first := filepath.Join(dir, "first.csv")
	second := filepath.Join(dir, "second.csv")
	writePartitionedMultiCSV(t, left, "l1,2026-01-01,100,REF-1\nl2,2026-01-01,200,REF-2\n")
	writePartitionedMultiCSV(t, first, "a1,2026-01-01,100,REF-1\n")
	writePartitionedMultiCSV(t, second, "b1,2026-01-01,200,REF-2\nb2,2026-01-01,100,REF-1\n")
	cfg := partitionedMultiCSVConfig()
	pair := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0}

	var baseline bytes.Buffer
	baselineWriter, err := NewResultWriter("json", &baseline)
	if err != nil {
		t.Fatal(err)
	}
	indexes := []RightIndex{NewMemoryIndex(), NewMemoryIndex()}
	defer func() {
		for _, index := range indexes {
			_ = index.Close()
		}
	}()
	if err := ReconcileStreamingMultiSource(context.Background(), "p", "left", left, cfg, []CounterpartStream{
		{SourceName: "first", RightPath: first, RightCfg: cfg, Index: indexes[0]},
		{SourceName: "second", RightPath: second, RightCfg: cfg, Index: indexes[1]},
	}, pair, baselineWriter, 0); err != nil {
		t.Fatal(err)
	}

	var partitioned bytes.Buffer
	partitionedWriter, err := NewResultWriter("json", &partitioned)
	if err != nil {
		t.Fatal(err)
	}
	spill := filepath.Join(dir, "spill")
	err = ReconcilePartitionedMultiSourceWithOptions(context.Background(), "p", "left", left, cfg, []PartitionedCounterpartInput{
		{SourceName: "first", RightPath: first, ParserCfg: cfg},
		{SourceName: "second", RightPath: second, ParserCfg: cfg},
	}, pair, partitionedWriter, PartitionedOptions{Partitions: 2, SpillDir: spill})
	if err != nil {
		t.Fatal(err)
	}
	var want, got Result
	if err := json.Unmarshal(baseline.Bytes(), &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(partitioned.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Summary != want.Summary {
		t.Fatalf("summary mismatch: partitioned=%+v baseline=%+v", got.Summary, want.Summary)
	}
	if len(got.Matched) != 2 || len(got.UnmatchedRight) != 1 || len(got.UnmatchedLeft) != 0 {
		t.Fatalf("unexpected result: matched=%d unmatched_left=%d unmatched_right=%d", len(got.Matched), len(got.UnmatchedLeft), len(got.UnmatchedRight))
	}
	entries, err := os.ReadDir(spill)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("spill directory contains cleanup artifacts: %v", entries)
	}
}

func TestReconcilePartitionedMultiSourceDuplicateParityAcrossPartitions(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	first := filepath.Join(dir, "first.csv")
	second := filepath.Join(dir, "second.csv")
	refForPartition := func(ref string) int {
		h := fnv.New32a()
		_, _ = h.Write([]byte(ref))
		return int(h.Sum32() % 2)
	}
	firstRef, secondRef := "REF-0", "REF-1"
	for i := 1; refForPartition(firstRef) == refForPartition(secondRef); i++ {
		secondRef = fmt.Sprintf("REF-%d", i)
	}
	header := "id,date,amount,reference,group\n"
	if err := os.WriteFile(left, []byte(header+
		"l1,2026-01-01,100,"+firstRef+",DUP-LEFT\n"+
		"l2,2026-01-01,200,"+secondRef+",DUP-LEFT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, []byte(header+"a1,2026-01-01,100,"+firstRef+",UNIQUE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte(header+"b1,2026-01-01,200,"+secondRef+",UNIQUE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", Multiplier: 1, RefCol: "reference", GroupCol: "group"}
	pair := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0}

	var baseline bytes.Buffer
	bw, err := NewResultWriter("json", &baseline)
	if err != nil {
		t.Fatal(err)
	}
	indexes := []RightIndex{NewMemoryIndex(), NewMemoryIndex()}
	if err := ReconcileStreamingMultiSource(context.Background(), "p", "left", left, cfg, []CounterpartStream{
		{SourceName: "first", RightPath: first, RightCfg: cfg, Index: indexes[0]},
		{SourceName: "second", RightPath: second, RightCfg: cfg, Index: indexes[1]},
	}, pair, bw, 0); err != nil {
		t.Fatal(err)
	}
	for _, index := range indexes {
		if err := index.Close(); err != nil {
			t.Fatal(err)
		}
	}

	var partitioned bytes.Buffer
	pw, err := NewResultWriter("json", &partitioned)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReconcilePartitionedMultiSourceWithOptions(context.Background(), "p", "left", left, cfg, []PartitionedCounterpartInput{
		{SourceName: "first", RightPath: first, ParserCfg: cfg},
		{SourceName: "second", RightPath: second, ParserCfg: cfg},
	}, pair, pw, PartitionedOptions{Partitions: 2, SpillDir: filepath.Join(dir, "spill")}); err != nil {
		t.Fatal(err)
	}
	assertJSONResultsMatch(t, baseline.Bytes(), partitioned.Bytes())
}

func TestReconcilePartitionedMultiSourceGroupedCarryForward(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	first := filepath.Join(dir, "first.csv")
	second := filepath.Join(dir, "second.csv")
	writePartitionedMultiCSV(t, left, "l1,2026-01-01,300,G1\nl2,2026-01-01,400,G2\n")
	writePartitionedMultiCSV(t, first, "a1,2026-01-01,100,G1\na2,2026-01-01,200,G1\n")
	writePartitionedMultiCSV(t, second, "b1,2026-01-01,150,G2\nb2,2026-01-01,250,G2\n")
	cfg := partitionedMultiCSVConfig()
	pair := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0, Passes: []config.PassConfig{{Type: config.PassTypeOneToMany}}}
	var out bytes.Buffer
	w, err := NewResultWriter("json", &out)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReconcilePartitionedMultiSourceWithOptions(context.Background(), "p", "left", left, cfg, []PartitionedCounterpartInput{
		{SourceName: "first", RightPath: first, ParserCfg: cfg},
		{SourceName: "second", RightPath: second, ParserCfg: cfg},
	}, pair, w, PartitionedOptions{Partitions: 2, SpillDir: filepath.Join(dir, "spill")}); err != nil {
		t.Fatal(err)
	}
	var result Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Summary.GroupedMatchedCount != 2 || len(result.GroupedMatched) != 2 {
		t.Fatalf("grouped result=%+v", result.Summary)
	}
	if result.Summary.UnmatchedLeft != 0 || result.Summary.UnmatchedRight != 0 {
		t.Fatalf("carry-forward left/right unmatched: %+v", result.Summary)
	}
	if result.BySource["first"].GroupedMatchedCount != 1 || result.BySource["second"].GroupedMatchedCount != 1 {
		t.Fatalf("by-source summaries=%+v", result.BySource)
	}
}

func TestReconcilePartitionedMultiSourceManyToManyCarryForward(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	first := filepath.Join(dir, "first.csv")
	second := filepath.Join(dir, "second.csv")
	writePartitionedMultiCSV(t, left, "l1,2026-01-01,70,G1\nl2,2026-01-01,30,G1\nl3,2026-01-01,100,G2\n")
	writePartitionedMultiCSV(t, first, "a1,2026-01-01,50,G1\na2,2026-01-01,50,G1\n")
	writePartitionedMultiCSV(t, second, "b1,2026-01-01,100,G2\n")
	cfg := partitionedMultiCSVConfig()
	pair := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0, Passes: []config.PassConfig{{Type: config.PassTypeManyToMany}}}
	var out bytes.Buffer
	w, err := NewResultWriter("json", &out)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReconcilePartitionedMultiSourceWithOptions(context.Background(), "p", "left", left, cfg, []PartitionedCounterpartInput{
		{SourceName: "first", RightPath: first, ParserCfg: cfg},
		{SourceName: "second", RightPath: second, ParserCfg: cfg},
	}, pair, w, PartitionedOptions{Partitions: 2, SpillDir: filepath.Join(dir, "spill")}); err != nil {
		t.Fatal(err)
	}
	var result Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Summary.ManyToManyMatchedCount != 2 || len(result.ManyToManyMatched) != 2 {
		t.Fatalf("many-to-many result=%+v", result.Summary)
	}
	if result.Summary.UnmatchedLeft != 0 || result.Summary.UnmatchedRight != 0 {
		t.Fatalf("many-to-many carry-forward unmatched: %+v", result.Summary)
	}
}

func TestReconcilePartitionedMultiSourceCleansSpillOnFailure(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	missing := filepath.Join(dir, "missing.csv")
	writePartitionedMultiCSV(t, left, "l1,2026-01-01,100,REF-1\n")
	spill := filepath.Join(dir, "spill")
	w, err := NewResultWriter("ndjson", &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	err = ReconcilePartitionedMultiSourceWithOptions(context.Background(), "p", "left", left, partitionedMultiCSVConfig(), []PartitionedCounterpartInput{
		{SourceName: "missing", RightPath: missing, ParserCfg: partitionedMultiCSVConfig()},
	}, config.Pair{}, w, PartitionedOptions{Partitions: 2, SpillDir: spill})
	if err == nil {
		t.Fatal("expected missing counterpart error")
	}
	entries, readErr := os.ReadDir(spill)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("spill directory contains cleanup artifacts: %v", entries)
	}
}

func TestReconcilePartitionedCounterpartPassRemovesConsumedPartitions(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")
	writePartitionedMultiCSV(t, left, "l1,2026-01-01,100,REF-1\nl2,2026-01-01,200,REF-2\n")
	writePartitionedMultiCSV(t, right, "r1,2026-01-01,100,REF-1\n")
	cfg := partitionedMultiCSVConfig()
	leftParts, err := partitionCSVWithSidecars(context.Background(), left, cfg.RefCol, filepath.Join(dir, "left-parts"), 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	rightParts, err := partitionCSVWithSidecars(context.Background(), right, cfg.RefCol, filepath.Join(dir, "right-parts"), 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewResultWriter("ndjson", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	next, _, err := reconcilePartitionedCounterpartPass(
		context.Background(), "p", "left", cfg, leftParts, "right", cfg, rightParts,
		config.Pair{}, w, PartitionedOptions{}, 0, dir, nil, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range append(append(append([]string{}, leftParts.data...), leftParts.rows...), append(rightParts.data, rightParts.rows...)...) {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("consumed partition still exists: %s (err=%v)", path, err)
		}
	}
	for _, path := range append(append([]string{}, next.data...), next.rows...) {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("carry-forward partition missing: %s: %v", path, err)
		}
	}
}

func TestReconcilePartitionedGroupedCounterpartPassRemovesSortOutputs(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")
	writePartitionedMultiCSV(t, left, "l1,2026-01-01,300,G1\n")
	writePartitionedMultiCSV(t, right, "r1,2026-01-01,100,G1\nr2,2026-01-01,200,G1\n")
	cfg := partitionedMultiCSVConfig()
	leftParts, err := partitionCSVWithSidecars(context.Background(), left, cfg.RefCol, filepath.Join(dir, "left-parts"), 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	rightParts, err := partitionCSVWithSidecars(context.Background(), right, cfg.RefCol, filepath.Join(dir, "right-parts"), 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewResultWriter("ndjson", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	pair := config.Pair{Passes: []config.PassConfig{{Type: config.PassTypeOneToMany}}}
	if _, _, err := reconcilePartitionedCounterpartPass(
		context.Background(), "p", "left", cfg, leftParts, "right", cfg, rightParts,
		pair, w, PartitionedOptions{}, 0, dir, nil, nil, nil,
	); err != nil {
		t.Fatal(err)
	}
	sorted, err := filepath.Glob(filepath.Join(dir, "sorted-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sorted) != 0 {
		t.Fatalf("grouped sort outputs remain after pass: %v", sorted)
	}
}

func TestReconcilePartitionedCounterpartPassPropagatesGroupedSortError(t *testing.T) {
	dir := t.TempDir()
	leftData := filepath.Join(dir, "left.csv")
	rightData := filepath.Join(dir, "right.csv")
	leftRows := filepath.Join(dir, "left.rows")
	rightRows := filepath.Join(dir, "right.rows")
	writePartitionedMultiCSV(t, leftData, "l1,2026-01-01,300,G1\n")
	writePartitionedMultiCSV(t, rightData, "r1,2026-01-01,300,G1\n")
	if err := os.WriteFile(leftRows, []byte("not-a-row-number\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightRows, []byte("2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := NewResultWriter("ndjson", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	pair := config.Pair{Passes: []config.PassConfig{{Type: config.PassTypeOneToMany}}}
	cfg := partitionedMultiCSVConfig()
	_, _, err = reconcilePartitionedCounterpartPass(
		context.Background(), "p", "left", cfg,
		groupedPartitionFiles{data: []string{leftData}, rows: []string{leftRows}, count: 1},
		"right", cfg,
		groupedPartitionFiles{data: []string{rightData}, rows: []string{rightRows}, count: 1},
		pair, w, PartitionedOptions{}, 0, dir, nil, nil, nil,
	)
	if err == nil || !strings.Contains(err.Error(), "invalid partition row number") {
		t.Fatalf("error=%v, want grouped sort error to reach pass caller", err)
	}
}

func partitionedMultiCSVConfig() config.ParserCfg {
	return config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", Multiplier: 1, RefCol: "reference", GroupCol: "reference"}
}

func writePartitionedMultiCSV(t *testing.T, path, rows string) {
	t.Helper()
	content := "id,date,amount,reference\n" + rows
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
