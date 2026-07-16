//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package reconcile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/reconifyhq/reconify/config"
	. "github.com/reconifyhq/reconify/engine/domain"
)

func TestReconcilePartitionedMatchesStreamingResults(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")
	write := func(path string, n int) {
		f, err := os.Create(path) // #nosec G304 -- test path is created under t.TempDir().
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		fmt.Fprintln(f, "date,amount,currency,reference")
		for i := 0; i < n; i++ {
			fmt.Fprintf(f, "2026-01-01,%d,USD,REF-%03d\n", 100+i, i)
		}
	}
	write(left, 100)
	write(right, 90)
	cfg := config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", CurrencyCol: "currency", RefCol: "reference", SkipRaw: true}
	pair := config.Pair{Left: "left", Right: "right"}

	var baseline bytes.Buffer
	bw, err := NewResultWriter("ndjson", &baseline)
	if err != nil {
		t.Fatal(err)
	}
	idx := NewMemoryIndex()
	if err := ReconcileStreaming(context.Background(), "p", "left", "right", left, right, cfg, cfg, pair, idx, bw, 0); err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}

	var partitioned bytes.Buffer
	pw, err := NewResultWriter("ndjson", &partitioned)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReconcilePartitioned(context.Background(), "p", "left", "right", left, right, cfg, cfg, pair, pw, 0, 4); err != nil {
		t.Fatal(err)
	}
	baseSummary := lastSummaryLine(baseline.String())
	partSummary := lastSummaryLine(partitioned.String())
	if baseSummary != partSummary {
		t.Fatalf("summary mismatch\nbaseline: %s\npartitioned: %s", baseSummary, partSummary)
	}
	baseEvents := sortedEventLines(baseline.String())
	partitionedEvents := sortedEventLines(partitioned.String())
	if strings.Join(baseEvents, "\n") != strings.Join(partitionedEvents, "\n") {
		t.Fatalf("event output mismatch\nbaseline: %s\npartitioned: %s", strings.Join(baseEvents, "\n"), strings.Join(partitionedEvents, "\n"))
	}

	var telemetry []TelemetryEvent
	telemetryWriter, err := NewResultWriter("ndjson", &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ReconcilePartitionedWithTelemetry(context.Background(), "p", "left", "right", left, right, cfg, cfg, pair, telemetryWriter, 0, 4, TelemetryOptions{
		ProgressEvery: 1,
		Sink: func(event TelemetryEvent) error {
			telemetry = append(telemetry, event)
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	for _, event := range telemetry {
		if event.Stage == "partitioning" && event.Status == "completed" && event.Rows == 190 {
			return
		}
	}
	t.Fatalf("partition telemetry did not report input rows: %#v", telemetry)
}

func TestReconcilePartitionedWithOptionsCleansConfiguredSpillDir(t *testing.T) {
	dir := t.TempDir()
	spill := filepath.Join(dir, "spill")
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")
	write := func(path string) {
		f, err := os.Create(path) // #nosec G304 -- test path is created under t.TempDir().
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		fmt.Fprintln(f, "date,amount,reference")
		fmt.Fprintln(f, "2026-01-01,100,REF-1")
	}
	write(left)
	write(right)
	cfg := config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", RefCol: "Reference"}
	var output bytes.Buffer
	w, err := NewResultWriter("ndjson", &output)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReconcilePartitionedWithOptions(context.Background(), "p", "left", "right", left, right, cfg, cfg, config.Pair{}, w, PartitionedOptions{Partitions: 2, SpillDir: spill}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(spill)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("spill entries=%d, want cleanup", len(entries))
	}
}

func TestReconcilePartitionedChunkLimitFailsAndCleansSpill(t *testing.T) {
	dir := t.TempDir()
	spill := filepath.Join(dir, "spill")
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")
	content := "date,amount,reference\n2026-01-01,100,REF-1\n"
	if err := os.WriteFile(left, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(right, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", RefCol: "reference"}
	var output bytes.Buffer
	w, err := NewResultWriter("ndjson", &output)
	if err != nil {
		t.Fatal(err)
	}
	err = ReconcilePartitionedWithOptions(context.Background(), "p", "left", "right", left, right, cfg, cfg, config.Pair{}, w, PartitionedOptions{
		Partitions: 2, Workers: 2, QueueCapacity: 1, MaxChunkBytes: 1, SpillDir: spill,
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds configured max bytes") {
		t.Fatalf("error=%v, want chunk size safeguard", err)
	}
	entries, readErr := os.ReadDir(spill)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("spill entries=%d, want cleanup", len(entries))
	}
}

func TestOpenGroupedRunCursorsClosesEarlierRunsOnOpenFailure(t *testing.T) {
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "run-0.gob"), filepath.Join(dir, "run-1.gob")}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("run"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var opened []*os.File
	_, _, err := openGroupedRunCursorsWith(paths, func(path string) (*os.File, error) {
		if len(opened) == 1 {
			return nil, errors.New("injected later run open failure")
		}
		f, openErr := os.Open(path) // #nosec G304 -- test paths are created under t.TempDir().
		if openErr == nil {
			opened = append(opened, f)
		}
		return f, openErr
	}, func(*groupedRunCursor) error {
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "injected later run open failure") {
		t.Fatalf("error = %v, want injected open failure", err)
	}
	if len(opened) != 1 {
		t.Fatalf("opened files = %d, want one earlier run", len(opened))
	}
	if closeErr := opened[0].Close(); closeErr == nil {
		t.Fatal("earlier run file remained open after setup failure")
	}
}

func TestSortGroupedPartitionReturnsSidecarError(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "partition.csv")
	rowsPath := filepath.Join(dir, "partition.rows")
	if err := os.WriteFile(dataPath, []byte("date,amount,reference\n2026-01-01,100,REF-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rowsPath, []byte("not-a-row-number\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := sortGroupedPartition(context.Background(), dataPath, rowsPath, filepath.Join(dir, "sorted"), "reference")
	if err == nil || !strings.Contains(err.Error(), "invalid partition row number") {
		t.Fatalf("error=%v, want sidecar sort error", err)
	}
}

func TestOpenGroupedRunCursorsClosesEarlierRunsOnPrimeFailure(t *testing.T) {
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "run-0.gob"), filepath.Join(dir, "run-1.gob")}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("run"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var opened []*os.File
	_, _, err := openGroupedRunCursorsWith(paths, func(path string) (*os.File, error) {
		f, openErr := os.Open(path) // #nosec G304 -- test paths are created under t.TempDir().
		if openErr == nil {
			opened = append(opened, f)
		}
		return f, openErr
	}, func(cursor *groupedRunCursor) error {
		if cursor.index == 1 {
			return errors.New("injected later run prime failure")
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "injected later run prime failure") {
		t.Fatalf("error = %v, want injected prime failure", err)
	}
	if len(opened) != 2 {
		t.Fatalf("opened files = %d, want two primed runs", len(opened))
	}
	for i, file := range opened {
		if closeErr := file.Close(); closeErr == nil {
			t.Fatalf("run %d remained open after setup failure", i)
		}
	}
}

func TestReconcilePartitionedCancellationCleansConfiguredSpillDir(t *testing.T) {
	dir := t.TempDir()
	spill := filepath.Join(dir, "spill")
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")
	content := "date,amount,reference\n2026-01-01,100,REF-1\n"
	for _, path := range []string{left, right} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", RefCol: "reference"}
	w, err := NewResultWriter("ndjson", &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = ReconcilePartitionedWithOptionsAndTelemetry(ctx, "p", "left", "right", left, right, cfg, cfg, config.Pair{}, w, PartitionedOptions{
		Partitions: 2,
		SpillDir:   spill,
	}, TelemetryOptions{
		ProgressEvery: 1,
		Sink: func(event TelemetryEvent) error {
			if event.Stage == "partitioning" && event.Status == "running" {
				cancel()
			}
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	entries, readErr := os.ReadDir(spill)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("spill entries=%d, want cleanup", len(entries))
	}
}

func TestReconcilePartitionedParallelTelemetryIncludesPartitionMatchStages(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")
	content := "date,amount,reference\n2026-01-01,100,REF-1\n2026-01-01,200,REF-2\n"
	if err := os.WriteFile(left, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(right, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", RefCol: "reference"}
	w, err := NewResultWriter("ndjson", &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	var events []TelemetryEvent
	err = ReconcilePartitionedWithOptionsAndTelemetry(context.Background(), "p", "left", "right", left, right, cfg, cfg, config.Pair{DateWindow: "0d"}, w, PartitionedOptions{
		Partitions: 2, Workers: 2, QueueCapacity: 1, SpillDir: filepath.Join(dir, "spill"),
	}, TelemetryOptions{
		ProgressEvery: 1,
		Sink: func(event TelemetryEvent) error {
			events = append(events, event)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := map[string]int{}
	for _, event := range events {
		if event.Status == "completed" {
			completed[event.Stage]++
		}
	}
	for _, stage := range []string{"right_index", "left_match", "finalization"} {
		if completed[stage] == 0 {
			t.Fatalf("missing completed %s telemetry event: %#v", stage, events)
		}
	}
}

func TestReconcilePartitionedRejectsMixedCurrenciesAcrossPartitions(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")
	if err := os.WriteFile(left, []byte("date,amount,currency,reference\n2026-01-01,100,USD,A\n2026-01-01,200,EUR,B\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(right, []byte("date,amount,currency,reference\n2026-01-01,100,USD,A\n2026-01-01,200,EUR,B\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", CurrencyCol: "currency", RefCol: "reference", SkipRaw: true}
	w, err := NewResultWriter("ndjson", &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	err = ReconcilePartitioned(context.Background(), "p", "left", "right", left, right, cfg, cfg, config.Pair{}, w, 0, 2)
	if err == nil || !strings.Contains(err.Error(), "mixed currencies") {
		t.Fatalf("error=%v, want mixed-currency error", err)
	}
}

func TestReconcilePartitionedRejectsUngroupedDuplicateKeyMismatch(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")
	content := "date,amount,reference,group\n2026-01-01,100,A,G\n"
	if err := os.WriteFile(left, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(right, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", RefCol: "reference", GroupCol: "group", SkipRaw: true}
	pair := config.Pair{Passes: []config.PassConfig{{Type: config.PassTypeOneToMany, GroupBy: config.GroupByReference}}}
	w, err := NewResultWriter("ndjson", &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	err = ReconcilePartitioned(context.Background(), "p", "left", "right", left, right, cfg, cfg, pair, w, 0, 2)
	if err == nil || !strings.Contains(err.Error(), "duplicate groups use") {
		t.Fatalf("error=%v, want duplicate co-location error", err)
	}
}

func TestReconcilePartitionedTrimsReferenceBeforeHashing(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")
	if err := os.WriteFile(left, []byte("date,amount,reference\n2026-01-01,100,\" REF-1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(right, []byte("date,amount,reference\n2026-01-01,100,REF-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", RefCol: "reference"}
	var output bytes.Buffer
	w, err := NewResultWriter("ndjson", &output)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReconcilePartitioned(context.Background(), "p", "left", "right", left, right, cfg, cfg, config.Pair{}, w, 0, 2); err != nil {
		t.Fatal(err)
	}
	if summary := lastSummaryLine(output.String()); !strings.Contains(summary, `"matched":1`) || strings.Contains(summary, `"unmatched_left":1`) {
		t.Fatalf("summary=%s, want one trimmed-reference match", summary)
	}
}

func TestReconcilePartitionedPreservesDuplicateGroupsAcrossPartitions(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")

	refForPartition := func(ref string) int {
		h := fnv.New32a()
		_, _ = h.Write([]byte(ref))
		return int(h.Sum32() % 2)
	}
	firstRef := "REF-0"
	secondRef := "REF-1"
	for i := 1; refForPartition(firstRef) == refForPartition(secondRef); i++ {
		secondRef = fmt.Sprintf("REF-%d", i)
	}

	if err := os.WriteFile(left, []byte("date,amount,reference,group\n2026-01-01,100,"+firstRef+",DUP-1\n2026-01-01,100,"+secondRef+",DUP-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(right, []byte("date,amount,reference,group\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.ParserCfg{
		Type:       "csv",
		DateCol:    "date",
		DateLayout: "2006-01-02",
		AmountCol:  "amount",
		RefCol:     "reference",
		GroupCol:   "group",
	}
	pair := config.Pair{Left: "left", Right: "right"}

	var baseline bytes.Buffer
	bw, err := NewResultWriter("ndjson", &baseline)
	if err != nil {
		t.Fatal(err)
	}
	idx := NewMemoryIndex()
	if err := ReconcileStreaming(context.Background(), "p", "left", "right", left, right, cfg, cfg, pair, idx, bw, 0); err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}

	var partitioned bytes.Buffer
	pw, err := NewResultWriter("ndjson", &partitioned)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReconcilePartitioned(context.Background(), "p", "left", "right", left, right, cfg, cfg, pair, pw, 0, 2); err != nil {
		t.Fatal(err)
	}
	if got, want := summaryFromNDJSON(partitioned.String()), summaryFromNDJSON(baseline.String()); got != want {
		t.Fatalf("summary mismatch\npartitioned: %s\nbaseline: %s", got, want)
	}
	if got := countNDJSONEvents(partitioned.String(), "duplicate"); got != 1 {
		t.Fatalf("duplicate events = %d, want 1", got)
	}
}

func TestReconcilePartitionedDuplicateJSONParityWithGroupColumn(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")
	refForPartition := func(ref string) int {
		h := fnv.New32a()
		_, _ = h.Write([]byte(ref))
		return int(h.Sum32() % 2)
	}
	firstRef := "REF-0"
	secondRef := "REF-1"
	for i := 1; refForPartition(firstRef) == refForPartition(secondRef); i++ {
		secondRef = fmt.Sprintf("REF-%d", i)
	}
	leftCSV := "date,amount,currency,reference,group\n" +
		"2026-01-01,100,USD," + firstRef + ",DUP-1\n" +
		"2026-01-01,200,USD," + secondRef + ",DUP-1\n" +
		"2026-01-01,300,USD,REF-UNIQUE,UNIQUE\n"
	rightCSV := "date,amount,currency,reference,group\n" +
		"2026-01-01,100,USD," + firstRef + ",DUP-RIGHT\n" +
		"2026-01-02,999,USD,REF-RIGHT-UNMATCHED,DUP-RIGHT\n"
	if err := os.WriteFile(left, []byte(leftCSV), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(right, []byte(rightCSV), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.ParserCfg{
		Type:        "csv",
		DateCol:     "date",
		DateLayout:  "2006-01-02",
		AmountCol:   "amount",
		CurrencyCol: "currency",
		RefCol:      "reference",
		GroupCol:    "group",
		SkipRaw:     true,
	}
	pair := config.Pair{Left: "left", Right: "right", DateWindow: "0d"}

	var baseline bytes.Buffer
	bw, err := NewResultWriter("json", &baseline)
	if err != nil {
		t.Fatal(err)
	}
	idx := NewMemoryIndex()
	if err := ReconcileStreaming(context.Background(), "p", "left", "right", left, right, cfg, cfg, pair, idx, bw, 0); err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}

	var partitioned bytes.Buffer
	pw, err := NewResultWriter("json", &partitioned)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReconcilePartitioned(context.Background(), "p", "left", "right", left, right, cfg, cfg, pair, pw, 0, 2); err != nil {
		t.Fatal(err)
	}
	assertJSONResultsMatch(t, baseline.Bytes(), partitioned.Bytes())
}

func TestReconcilePartitionedPreservesDuplicatePolicyAcrossPartitions(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")
	refForPartition := func(ref string) int {
		h := fnv.New32a()
		_, _ = h.Write([]byte(ref))
		return int(h.Sum32() % 2)
	}
	firstRef := "REF-0"
	secondRef := "REF-1"
	for i := 1; refForPartition(firstRef) == refForPartition(secondRef); i++ {
		secondRef = fmt.Sprintf("REF-%d", i)
	}
	input := "date,amount,reference,group\n2026-01-01,100," + firstRef + ",DUP-1\n2026-01-01,100," + secondRef + ",DUP-1\n"
	if err := os.WriteFile(left, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(right, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	pair := config.Pair{Left: "left", Right: "right"}
	for _, policy := range []config.DuplicatePolicy{config.DuplicatePolicyMerge, config.DuplicatePolicyLatest} {
		t.Run(string(policy), func(t *testing.T) {
			cfg := config.ParserCfg{
				Type:            "csv",
				DateCol:         "date",
				DateLayout:      "2006-01-02",
				AmountCol:       "amount",
				RefCol:          "reference",
				GroupCol:        "group",
				DuplicatePolicy: policy,
			}
			var baseline bytes.Buffer
			bw, err := NewResultWriter("ndjson", &baseline)
			if err != nil {
				t.Fatal(err)
			}
			idx := NewMemoryIndex()
			if err := ReconcileStreaming(context.Background(), "p", "left", "right", left, right, cfg, cfg, pair, idx, bw, 0); err != nil {
				t.Fatal(err)
			}
			if err := idx.Close(); err != nil {
				t.Fatal(err)
			}

			var partitioned bytes.Buffer
			pw, err := NewResultWriter("ndjson", &partitioned)
			if err != nil {
				t.Fatal(err)
			}
			if err := ReconcilePartitioned(context.Background(), "p", "left", "right", left, right, cfg, cfg, pair, pw, 0, 2); err != nil {
				t.Fatal(err)
			}
			if got, want := summaryFromNDJSON(partitioned.String()), summaryFromNDJSON(baseline.String()); got != want {
				t.Fatalf("summary mismatch\npartitioned: %s\nbaseline: %s", got, want)
			}
			gotEvents := sortedEventLines(partitioned.String())
			wantEvents := sortedEventLines(baseline.String())
			if strings.Join(gotEvents, "\n") != strings.Join(wantEvents, "\n") {
				t.Fatalf("event mismatch\npartitioned: %s\nbaseline: %s", strings.Join(gotEvents, "\n"), strings.Join(wantEvents, "\n"))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Grouped parity tests
// ---------------------------------------------------------------------------

func TestReconcilePartitionedMatchesBatchOneToMany(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")

	// Build data: each group has one left row and two right rows that sum to it.
	// Use several groups so they spread across partitions with n=4.
	type group struct{ ref string }
	groups := []group{{"G1"}, {"G2"}, {"G3"}, {"G4"}, {"G5"}, {"G6"}, {"G7"}, {"G8"}}

	lf, err := os.Create(left) // #nosec G304
	if err != nil {
		t.Fatal(err)
	}
	rf, err := os.Create(right) // #nosec G304
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(lf, "date,amount,reference")
	fmt.Fprintln(rf, "date,amount,reference")
	for i, g := range groups {
		// Left: one row, amount = 300.
		fmt.Fprintf(lf, "2026-01-01,300,%s\n", g.ref)
		// Right: two rows that sum to 300.
		fmt.Fprintf(rf, "2026-01-01,%d,%s\n", 100+i, g.ref)
		fmt.Fprintf(rf, "2026-01-01,%d,%s\n", 200-i, g.ref)
	}
	// Extra right row with no left → unmatched_right.
	fmt.Fprintln(rf, "2026-01-01,50,UNMATCHED")
	if err := lf.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rf.Close(); err != nil {
		t.Fatal(err)
	}

	cfg := config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", RefCol: "reference", SkipRaw: true}
	pair := config.Pair{
		Left:  "left",
		Right: "right",
		Passes: []config.PassConfig{
			{Type: config.PassTypeOneToMany},
		},
	}

	var baseline bytes.Buffer
	bw, err := NewResultWriter("ndjson", &baseline)
	if err != nil {
		t.Fatal(err)
	}
	baseResult, err := Reconcile("p", "left", "right", mustParse(t, "left", left, cfg), mustParse(t, "right", right, cfg), pair)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteResult(bw, baseResult); err != nil {
		t.Fatal(err)
	}

	// ndjson: line-by-line comparison of sorted events and summary.
	t.Run("ndjson", func(t *testing.T) {
		var partBuf bytes.Buffer
		pw, err := NewResultWriter("ndjson", &partBuf)
		if err != nil {
			t.Fatal(err)
		}
		if err := ReconcilePartitionedWithOptions(context.Background(), "p", "left", "right", left, right, cfg, cfg, pair, pw, PartitionedOptions{Partitions: 4}); err != nil {
			t.Fatal(err)
		}
		if got, want := lastSummaryLine(partBuf.String()), lastSummaryLine(baseline.String()); got != want {
			t.Fatalf("summary mismatch\npartitioned: %s\nbaseline: %s", got, want)
		}
		if got, want := sortedEventLines(partBuf.String()), sortedEventLines(baseline.String()); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("event mismatch: partitioned=%d baseline=%d", len(got), len(want))
		}
	})

	// json: compare full marshaled Result objects (json writer sorts deterministically).
	t.Run("json", func(t *testing.T) {
		var partBuf bytes.Buffer
		pw, err := NewResultWriter("json", &partBuf)
		if err != nil {
			t.Fatal(err)
		}
		if err := ReconcilePartitionedWithOptions(context.Background(), "p", "left", "right", left, right, cfg, cfg, pair, pw, PartitionedOptions{Partitions: 4}); err != nil {
			t.Fatal(err)
		}
		var baseBuf bytes.Buffer
		bw2, err := NewResultWriter("json", &baseBuf)
		if err != nil {
			t.Fatal(err)
		}
		if err := WriteResult(bw2, baseResult); err != nil {
			t.Fatal(err)
		}
		assertJSONResultsMatch(t, baseBuf.Bytes(), partBuf.Bytes())
	})
}

func TestReconcilePartitionedMatchesBatchManyToMany(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")

	lf, err := os.Create(left) // #nosec G304
	if err != nil {
		t.Fatal(err)
	}
	rf, err := os.Create(right) // #nosec G304
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(lf, "date,amount,reference")
	fmt.Fprintln(rf, "date,amount,reference")
	// Matched group: two lefts sum = two rights sum (both 300).
	for _, ref := range []string{"M1", "M2", "M3"} {
		fmt.Fprintf(lf, "2026-01-01,150,%s\n", ref)
		fmt.Fprintf(lf, "2026-01-01,150,%s\n", ref)
		fmt.Fprintf(rf, "2026-01-01,200,%s\n", ref)
		fmt.Fprintf(rf, "2026-01-01,100,%s\n", ref)
	}
	// Amount-diff group.
	fmt.Fprintln(lf, "2026-01-01,200,AD1")
	fmt.Fprintln(lf, "2026-01-01,100,AD1")
	fmt.Fprintln(rf, "2026-01-01,200,AD1")
	fmt.Fprintln(rf, "2026-01-01,50,AD1")
	if err := lf.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rf.Close(); err != nil {
		t.Fatal(err)
	}

	cfg := config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", RefCol: "reference", SkipRaw: true}
	pair := config.Pair{
		Left:  "left",
		Right: "right",
		Passes: []config.PassConfig{
			{Type: config.PassTypeManyToMany},
		},
	}

	var baseline bytes.Buffer
	bw, err := NewResultWriter("ndjson", &baseline)
	if err != nil {
		t.Fatal(err)
	}
	baseResult, err := Reconcile("p", "left", "right", mustParse(t, "left", left, cfg), mustParse(t, "right", right, cfg), pair)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteResult(bw, baseResult); err != nil {
		t.Fatal(err)
	}

	var partBuf bytes.Buffer
	pw, err := NewResultWriter("ndjson", &partBuf)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReconcilePartitionedWithOptions(context.Background(), "p", "left", "right", left, right, cfg, cfg, pair, pw, PartitionedOptions{Partitions: 4}); err != nil {
		t.Fatal(err)
	}

	baseSummary := lastSummaryLine(baseline.String())
	partSummary := lastSummaryLine(partBuf.String())
	if baseSummary != partSummary {
		t.Fatalf("summary mismatch\nbaseline: %s\npartitioned: %s", baseSummary, partSummary)
	}
	baseEvents := sortedEventLines(baseline.String())
	partEvents := sortedEventLines(partBuf.String())
	if strings.Join(baseEvents, "\n") != strings.Join(partEvents, "\n") {
		t.Fatalf("event mismatch: baseline=%d partitioned=%d", len(baseEvents), len(partEvents))
	}
}

func TestReconcilePartitionedGroupedOutcomesMatchBatch(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")
	leftData := strings.Join([]string{
		"date,amount,reference",
		"2026-01-01,300,MATCH",
		"2026-01-01,300,AMOUNT",
		"2026-01-01,300,TIMING",
		"2026-01-01,100,AMBIG",
		"2026-01-01,150,AMBIG",
		"2026-01-01,10,ONLY_LEFT",
		"",
	}, "\n")
	rightData := strings.Join([]string{
		"date,amount,reference",
		"2026-01-01,100,MATCH",
		"2026-01-01,200,MATCH",
		"2026-01-01,100,AMOUNT",
		"2026-01-01,100,AMOUNT",
		"2026-01-10,100,TIMING",
		"2026-01-10,200,TIMING",
		"2026-01-01,100,AMBIG",
		"2026-01-01,5,ONLY_RIGHT",
		"",
	}, "\n")
	if err := os.WriteFile(left, []byte(leftData), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(right, []byte(rightData), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", RefCol: "reference", SkipRaw: true}
	pair := config.Pair{
		Passes:     []config.PassConfig{{Type: config.PassTypeOneToMany}},
		DateWindow: "1d",
	}
	leftTxns := mustParse(t, "left", left, cfg)
	rightTxns := mustParse(t, "right", right, cfg)
	base, err := Reconcile("p", "left", "right", leftTxns, rightTxns, pair)
	if err != nil {
		t.Fatal(err)
	}
	var baseBuf bytes.Buffer
	baseWriter, err := NewResultWriter("json", &baseBuf)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteResult(baseWriter, base); err != nil {
		t.Fatal(err)
	}

	for _, partitions := range []int{2, 5} {
		t.Run(fmt.Sprintf("partitions_%d", partitions), func(t *testing.T) {
			var got bytes.Buffer
			writer, err := NewResultWriter("json", &got)
			if err != nil {
				t.Fatal(err)
			}
			if err := ReconcilePartitioned(context.Background(), "p", "left", "right", left, right, cfg, cfg, pair, writer, 0, partitions); err != nil {
				t.Fatal(err)
			}
			assertJSONResultsMatch(t, baseBuf.Bytes(), got.Bytes())
		})
	}
}

func TestReconcilePartitionedGroupedEmptyKeysAreStreamingUnmatched(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")
	if err := os.WriteFile(left, []byte("date,amount,reference\n2026-01-01,10,\n2026-01-01,20,\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(right, []byte("date,amount,reference\n2026-01-01,10,\n2026-01-01,30,\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", RefCol: "reference", SkipRaw: true}
	pair := config.Pair{Passes: []config.PassConfig{{Type: config.PassTypeManyToMany}}}
	var output bytes.Buffer
	writer, err := NewResultWriter("ndjson", &output)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReconcilePartitioned(context.Background(), "p", "left", "right", left, right, cfg, cfg, pair, writer, 0, 2); err != nil {
		t.Fatal(err)
	}
	summary := summaryFromNDJSON(output.String())
	if !strings.Contains(summary, `"unmatched_left":2`) || !strings.Contains(summary, `"unmatched_right":2`) {
		t.Fatalf("summary=%s, want all empty-key rows unmatched", summary)
	}
	if strings.Contains(output.String(), "many_to_many_match") || strings.Contains(output.String(), "grouped_match") {
		t.Fatalf("output=%s, empty keys must not produce grouped matches", output.String())
	}
}

func TestReconcilePartitionedGroupedSkewedGroupCleansSpill(t *testing.T) {
	dir := t.TempDir()
	spill := filepath.Join(dir, "spill")
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")
	lf, err := os.Create(left) // #nosec G304 -- test path is created under t.TempDir().
	if err != nil {
		t.Fatal(err)
	}
	rf, err := os.Create(right) // #nosec G304 -- test path is created under t.TempDir().
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintln(lf, "date,amount,reference")
	_, _ = fmt.Fprintln(rf, "date,amount,reference")
	const installments = 20_000
	_, _ = fmt.Fprintf(lf, "2026-01-01,%d,SKEW\n", installments)
	for i := 0; i < installments; i++ {
		_, _ = fmt.Fprintf(rf, "2026-01-01,1,SKEW\n")
	}
	if err := lf.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rf.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", RefCol: "reference", SkipRaw: true}
	pair := config.Pair{Passes: []config.PassConfig{{Type: config.PassTypeOneToMany}}}
	var output bytes.Buffer
	writer, err := NewResultWriter("ndjson", &output)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReconcilePartitionedWithOptions(context.Background(), "p", "left", "right", left, right, cfg, cfg, pair, writer, PartitionedOptions{Partitions: 2, SpillDir: spill}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summaryFromNDJSON(output.String()), `"grouped_matched_count":1`) {
		t.Fatalf("summary=%s, want one grouped match", summaryFromNDJSON(output.String()))
	}
	entries, err := os.ReadDir(spill)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("spill entries=%d, want cleanup", len(entries))
	}
}

func TestPartitionSummaryWriterWarnsUnsupportedGroupedEventsOnce(t *testing.T) {
	var output bytes.Buffer
	inner, err := NewResultWriter("csv", &output)
	if err != nil {
		t.Fatal(err)
	}
	observed := &warningCaptureWriter{ResultWriter: inner}
	agg := &partitionSummaryWriter{ResultWriter: observed}
	if err := agg.WriteGroupedMatch(GroupedMatchedPair{}); err != nil {
		t.Fatal(err)
	}
	if err := agg.WriteAmbiguousGroup(AmbiguousGroupPair{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(strings.Join(observed.warnings, "\n"), "does not support grouped or ambiguous match events"); got != 1 {
		t.Fatalf("grouped warning count=%d, want 1; warnings=%q", got, observed.warnings)
	}
}

type warningCaptureWriter struct {
	ResultWriter
	warnings []string
}

func (w *warningCaptureWriter) ObserveWarning(warning Warning) {
	w.warnings = append(w.warnings, warning.Message)
}

// TestPartitionKeyColumnsGating verifies gating logic for PartitionKeyColumns.
func TestPartitionKeyColumnsGating(t *testing.T) {
	refCfg := config.ParserCfg{RefCol: "ref", NameCol: "name", GroupCol: "grp"}
	noRefCfg := config.ParserCfg{NameCol: "name", GroupCol: "grp"}
	noNameCfg := config.ParserCfg{RefCol: "ref", GroupCol: "grp"}
	noGroupCfg := config.ParserCfg{RefCol: "ref", NameCol: "name"}

	tests := []struct {
		name    string
		pair    config.Pair
		left    config.ParserCfg
		right   config.ParserCfg
		wantOK  bool
		wantKey string // expected leftCol when ok
	}{
		{
			name: "no passes uses ref_col",
			pair: config.Pair{},
			left: refCfg, right: refCfg,
			wantOK: true, wantKey: "ref",
		},
		{
			name: "no passes missing ref_col rejected",
			pair: config.Pair{},
			left: noRefCfg, right: noRefCfg,
			wantOK: false,
		},
		{
			name: "reference_one_to_one uses ref_col",
			pair: config.Pair{Passes: []config.PassConfig{{Type: config.PassTypeReferenceOneToOne}}},
			left: refCfg, right: refCfg,
			wantOK: true, wantKey: "ref",
		},
		{
			name: "one_to_many default group_by reference uses ref_col",
			pair: config.Pair{Passes: []config.PassConfig{{Type: config.PassTypeOneToMany}}},
			left: refCfg, right: refCfg,
			wantOK: true, wantKey: "ref",
		},
		{
			name: "one_to_many group_by group_key uses group_col",
			pair: config.Pair{Passes: []config.PassConfig{{Type: config.PassTypeOneToMany, GroupBy: config.GroupByGroupKey}}},
			left: refCfg, right: refCfg,
			wantOK: true, wantKey: "grp",
		},
		{
			name: "one_to_many group_by name uses name_col",
			pair: config.Pair{Passes: []config.PassConfig{{Type: config.PassTypeOneToMany, GroupBy: config.GroupByName}}},
			left: refCfg, right: refCfg,
			wantOK: true, wantKey: "name",
		},
		{
			name: "mixed selectors rejected",
			pair: config.Pair{Passes: []config.PassConfig{{Type: config.PassTypeReferenceOneToOne}, {Type: config.PassTypeOneToMany, GroupBy: config.GroupByName}}},
			left: refCfg, right: refCfg,
			wantOK: false,
		},
		{
			name: "missing ref_col for reference pass rejected",
			pair: config.Pair{Passes: []config.PassConfig{{Type: config.PassTypeReferenceOneToOne}}},
			left: noRefCfg, right: noRefCfg,
			wantOK: false,
		},
		{
			name: "missing name_col for name pass rejected",
			pair: config.Pair{Passes: []config.PassConfig{{Type: config.PassTypeOneToMany, GroupBy: config.GroupByName}}},
			left: noNameCfg, right: noNameCfg,
			wantOK: false,
		},
		{
			name: "missing group_col for group_key pass rejected",
			pair: config.Pair{Passes: []config.PassConfig{{Type: config.PassTypeOneToMany, GroupBy: config.GroupByGroupKey}}},
			left: noGroupCfg, right: noGroupCfg,
			wantOK: false,
		},
		{
			name: "name_tokens_one_to_one pass rejected",
			pair: config.Pair{Passes: []config.PassConfig{{Type: config.PassTypeNameTokensOneToOne}}},
			left: refCfg, right: refCfg,
			wantOK: false,
		},
		{
			name: "name_mode tokens rejected",
			pair: config.Pair{NameMode: "tokens"},
			left: refCfg, right: refCfg,
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			leftCol, _, ok, reason := PartitionKeyColumns(tc.pair, tc.left, tc.right)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v reason=%q, want ok=%v", ok, reason, tc.wantOK)
			}
			if tc.wantOK && leftCol != tc.wantKey {
				t.Fatalf("leftCol=%q, want %q", leftCol, tc.wantKey)
			}
		})
	}
}

func mustParse(t *testing.T, sourceName, path string, cfg config.ParserCfg) []Transaction {
	t.Helper()
	txns, err := Parse(sourceName, path, cfg)
	if err != nil {
		t.Fatalf("parse %s: %v", sourceName, err)
	}
	return txns
}

// assertJSONResultsMatch compares two json-format Result documents by
// unmarshaling both, stripping generated IDs, sorting all arrays, and
// comparing canonical forms. Arrays are order-independent (partition order
// differs from batch order).
func assertJSONResultsMatch(t *testing.T, baseline, partitioned []byte) {
	t.Helper()
	var baseObj, partObj any
	if err := json.Unmarshal(baseline, &baseObj); err != nil {
		t.Fatalf("unmarshal baseline json: %v", err)
	}
	if err := json.Unmarshal(partitioned, &partObj); err != nil {
		t.Fatalf("unmarshal partitioned json: %v", err)
	}
	sortJSONArrays(baseObj)
	sortJSONArrays(partObj)
	baseCanon, _ := json.Marshal(baseObj)
	partCanon, _ := json.Marshal(partObj)
	if string(baseCanon) != string(partCanon) {
		t.Fatalf("json result mismatch\nbaseline:    %s\npartitioned: %s", baseCanon, partCanon)
	}
}

// sortJSONArrays sorts every array in the JSON value by its canonical JSON
// representation so that order-independent comparisons succeed.
func sortJSONArrays(v any) {
	switch val := v.(type) {
	case map[string]any:
		for k, child := range val {
			if arr, ok := child.([]any); ok {
				for _, elem := range arr {
					sortJSONArrays(elem)
				}
				sort.Slice(arr, func(i, j int) bool {
					bi, _ := json.Marshal(arr[i])
					bj, _ := json.Marshal(arr[j])
					return string(bi) < string(bj)
				})
				val[k] = arr
			} else {
				sortJSONArrays(child)
			}
		}
	case []any:
		for _, elem := range val {
			sortJSONArrays(elem)
		}
	}
}

func lastSummaryLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], `"type":"summary"`) {
			return lines[i]
		}
	}
	return ""
}

func summaryFromNDJSON(s string) string { return lastSummaryLine(s) }

func countNDJSONEvents(s, wantType string) int {
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(line), &event) == nil && event.Type == wantType {
			count++
		}
	}
	return count
}

func sortedEventLines(s string) []string {
	var events []string
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		var event struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil || event.Type == "summary" || event.Type == "" {
			continue
		}
		var value any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			continue
		}
		canonical, err := json.Marshal(value)
		if err == nil {
			events = append(events, string(canonical))
		}
	}
	sort.Strings(events)
	return events
}
