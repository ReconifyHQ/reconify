// Package telemetry emits optional reconciliation lifecycle events.
//
//nolint:revive // This package preserves stable compatibility names.
//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package telemetry

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	//nolint:staticcheck // Domain types are deliberately imported into the implementation namespace.
	. "github.com/reconifyhq/reconify/engine/domain"
)

// TelemetryEvent is an observational lifecycle record for a reconciliation run.
// Nil numeric fields mean the value cannot be known without an additional scan.
type TelemetryEvent struct {
	Type          string    `json:"type"`
	RunID         string    `json:"run_id"`
	Timestamp     time.Time `json:"timestamp"`
	Stage         string    `json:"stage"`
	Status        string    `json:"status"`
	Source        string    `json:"source,omitempty"`
	Counterpart   string    `json:"counterpart,omitempty"`
	Rows          int       `json:"rows"`
	TotalRows     *int      `json:"total_rows,omitempty"`
	Percentage    *float64  `json:"percentage,omitempty"`
	RowsPerSecond float64   `json:"rows_per_second"`
	Elapsed       float64   `json:"elapsed_seconds"`
	ETA           *float64  `json:"eta_seconds,omitempty"`
	RSSBytes      *uint64   `json:"rss_bytes,omitempty"`
	CPUSeconds    *float64  `json:"cpu_seconds,omitempty"`
	HeapBytes     *uint64   `json:"heap_bytes,omitempty"`
	GCCycles      *uint32   `json:"gc_cycles,omitempty"`
	Warning       *Warning  `json:"warning,omitempty"`
}

func (r *Reporter) Warning(warning Warning) {
	if r == nil {
		return
	}
	var onError func(error)
	var sinkErr error
	r.mu.Lock()
	if r.disabled || r.options.Sink == nil || len(r.stages) == 0 {
		r.mu.Unlock()
		return
	}
	stage := r.stages[len(r.stages)-1]
	event := TelemetryEvent{
		Type: "warning", RunID: r.options.RunID, Timestamp: time.Now().UTC(),
		Stage: stage.stage, Status: "observed", Source: stage.source,
		Counterpart: stage.counterpart, Rows: stage.rows, Warning: &warning,
	}
	sinkErr = r.options.Sink(event)
	if sinkErr != nil {
		r.disabled = true
		onError = r.options.OnError
	}
	r.mu.Unlock()
	if sinkErr != nil && onError != nil {
		onError(sinkErr)
	}
}

// TelemetrySink receives machine-readable lifecycle records. A sink failure is
// non-fatal: reconciliation continues and the error is reported once.
type TelemetrySink func(TelemetryEvent) error

// TelemetryOptions configures optional reconciliation telemetry. ProgressEvery
// controls row-based progress records; HeartbeatEvery controls wall-clock
// heartbeat records. A nil Sink disables telemetry entirely.
type TelemetryOptions struct {
	RunID          string
	ProgressEvery  int
	HeartbeatEvery time.Duration
	Sink           TelemetrySink
	OnError        func(error)
}

// ProgressEvent reports incremental progress for compatibility callbacks.
type ProgressEvent struct {
	Phase   string
	Rows    int
	Elapsed time.Duration
	Done    bool
}

// ProgressFunc receives compatibility progress callbacks.
type ProgressFunc func(ProgressEvent)

type Reporter struct {
	options TelemetryOptions
	started time.Time

	mu       sync.Mutex
	disabled bool
	stages   []telemetryStage
	stop     chan struct{}
	done     chan struct{}
}

type telemetryStage struct {
	stage       string
	source      string
	counterpart string
	rows        int
	total       *int
	startedAt   time.Time
	terminal    bool
}

var telemetrySequence uint64

// NewTelemetryRunID returns a unique identifier that callers can reuse across
// several telemetry-enabled operations in one reconciliation run.
func NewTelemetryRunID() string {
	return fmt.Sprintf("reconify-%d-%d", time.Now().UTC().UnixNano(), atomic.AddUint64(&telemetrySequence, 1))
}

func NewReporter(options TelemetryOptions) *Reporter {
	if options.Sink == nil {
		return nil
	}
	if options.ProgressEvery <= 0 {
		options.ProgressEvery = 1_000_000
	}
	if options.RunID == "" {
		options.RunID = NewTelemetryRunID()
	}
	r := &Reporter{
		options: options,
		started: time.Now(),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	if options.HeartbeatEvery > 0 {
		go r.heartbeatLoop(options.HeartbeatEvery)
	} else {
		close(r.done)
	}
	return r
}

func (r *Reporter) heartbeatLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer close(r.done)
	for {
		select {
		case <-ticker.C:
			r.emit("heartbeat", "running")
		case <-r.stop:
			return
		}
	}
}

func (r *Reporter) Close() {
	if r == nil {
		return
	}
	select {
	case <-r.stop:
	default:
		close(r.stop)
	}
	<-r.done
}

func (r *Reporter) Start(stage, source, counterpart string, total *int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.stages = append(r.stages, telemetryStage{
		stage:       stage,
		source:      source,
		counterpart: counterpart,
		total:       total,
		startedAt:   time.Now(),
	})
	r.mu.Unlock()
	r.emit("progress", "started")
}

func (r *Reporter) Progress(rows int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if len(r.stages) == 0 {
		r.mu.Unlock()
		return
	}
	for i := range r.stages {
		r.stages[i].rows = rows
	}
	every := r.options.ProgressEvery
	r.mu.Unlock()
	if rows > 0 && rows%every == 0 {
		r.emit("progress", "running")
	}
}

func (r *Reporter) Complete(rows int) {
	if r == nil {
		return
	}
	r.finish("completed", rows, true)
}

func (r *Reporter) Fail(rows int) {
	if r == nil {
		return
	}
	// A zero value means that the caller does not have a more precise count.
	// Keep the last count recorded by progress in that case.
	for {
		r.mu.Lock()
		active := len(r.stages) > 0
		r.mu.Unlock()
		if !active {
			return
		}
		r.finish("failed", rows, rows > 0)
		rows = 0
	}
}

func (r *Reporter) emit(eventType, status string) {
	if r == nil {
		return
	}
	r.emitEvent(eventType, status, false, 0, false)
}

func (r *Reporter) finish(status string, rows int, setRows bool) {
	r.emitEvent("progress", status, true, rows, setRows)
}

func (r *Reporter) emitEvent(eventType, status string, terminal bool, rows int, setRows bool) {
	var onError func(error)
	var sinkErr error

	r.mu.Lock()
	if r.disabled || r.options.Sink == nil || len(r.stages) == 0 {
		if terminal && len(r.stages) > 0 {
			r.stages = r.stages[:len(r.stages)-1]
		}
		r.mu.Unlock()
		return
	}
	stageIndex := len(r.stages) - 1
	stage := &r.stages[stageIndex]
	if terminal && stage.terminal {
		r.mu.Unlock()
		return
	}
	if setRows {
		stage.rows = rows
		for i := 0; i < stageIndex; i++ {
			r.stages[i].rows = rows
		}
	}
	now := time.Now()
	elapsed := now.Sub(stage.startedAt)
	event := TelemetryEvent{
		Type:        eventType,
		RunID:       r.options.RunID,
		Timestamp:   now.UTC(),
		Stage:       stage.stage,
		Status:      status,
		Source:      stage.source,
		Counterpart: stage.counterpart,
		Rows:        stage.rows,
		Elapsed:     elapsed.Seconds(),
	}
	if elapsed > 0 {
		event.RowsPerSecond = float64(stage.rows) / elapsed.Seconds()
	}
	if stage.total != nil {
		total := *stage.total
		event.TotalRows = &total
		if total > 0 {
			pct := float64(stage.rows) * 100 / float64(total)
			event.Percentage = &pct
			if event.RowsPerSecond > 0 && stage.rows <= total {
				eta := float64(total-stage.rows) / event.RowsPerSecond
				event.ETA = &eta
			}
		}
	}
	metrics := sampleResources()
	event.RSSBytes, event.CPUSeconds = metrics.rssBytes, metrics.cpuSeconds
	event.HeapBytes, event.GCCycles = metrics.heapBytes, metrics.gcCycles
	if terminal {
		stage.terminal = true
	}
	sinkErr = r.options.Sink(event)
	if sinkErr != nil {
		r.disabled = true
		onError = r.options.OnError
	}
	if terminal {
		r.stages = r.stages[:stageIndex]
	}
	r.mu.Unlock()

	if sinkErr != nil && onError != nil {
		onError(sinkErr)
	}
}

type resourceMetrics struct {
	rssBytes   *uint64
	cpuSeconds *float64
	heapBytes  *uint64
	gcCycles   *uint32
}
