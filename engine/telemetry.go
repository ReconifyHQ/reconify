package engine

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
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

type telemetryReporter struct {
	options TelemetryOptions
	started time.Time

	mu          sync.Mutex
	failed      bool
	stage       string
	source      string
	counterpart string
	rows        int
	total       *int
	stageAt     time.Time
	stop        chan struct{}
	done        chan struct{}
}

var telemetrySequence uint64

// NewTelemetryRunID returns a unique identifier that callers can reuse across
// several telemetry-enabled operations in one reconciliation run.
func NewTelemetryRunID() string {
	return fmt.Sprintf("reconify-%d-%d", time.Now().UTC().UnixNano(), atomic.AddUint64(&telemetrySequence, 1))
}

func newTelemetryReporter(options TelemetryOptions) *telemetryReporter {
	if options.Sink == nil {
		return nil
	}
	if options.ProgressEvery <= 0 {
		options.ProgressEvery = 1_000_000
	}
	if options.RunID == "" {
		options.RunID = NewTelemetryRunID()
	}
	r := &telemetryReporter{
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

func (r *telemetryReporter) heartbeatLoop(interval time.Duration) {
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

func (r *telemetryReporter) close() {
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

func (r *telemetryReporter) start(stage, source, counterpart string, total *int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.stage, r.source, r.counterpart, r.rows, r.total, r.stageAt = stage, source, counterpart, 0, total, time.Now()
	r.mu.Unlock()
	r.emit("progress", "started")
}

func (r *telemetryReporter) progress(rows int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.rows = rows
	every := r.options.ProgressEvery
	r.mu.Unlock()
	if rows > 0 && rows%every == 0 {
		r.emit("progress", "running")
	}
}

func (r *telemetryReporter) complete(rows int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.rows = rows
	r.mu.Unlock()
	r.emit("progress", "completed")
}

func (r *telemetryReporter) fail(rows int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.rows = rows
	r.mu.Unlock()
	r.emit("progress", "failed")
}

func (r *telemetryReporter) emit(eventType, status string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed || r.options.Sink == nil || r.stage == "" {
		return
	}
	now := time.Now()
	elapsed := now.Sub(r.stageAt)
	event := TelemetryEvent{
		Type:        eventType,
		RunID:       r.options.RunID,
		Timestamp:   now.UTC(),
		Stage:       r.stage,
		Status:      status,
		Source:      r.source,
		Counterpart: r.counterpart,
		Rows:        r.rows,
		Elapsed:     elapsed.Seconds(),
	}
	if elapsed > 0 {
		event.RowsPerSecond = float64(r.rows) / elapsed.Seconds()
	}
	if r.total != nil {
		total := *r.total
		event.TotalRows = &total
		if total > 0 {
			pct := float64(r.rows) * 100 / float64(total)
			event.Percentage = &pct
			if event.RowsPerSecond > 0 && r.rows <= total {
				eta := float64(total-r.rows) / event.RowsPerSecond
				event.ETA = &eta
			}
		}
	}
	metrics := sampleResources()
	event.RSSBytes, event.CPUSeconds = metrics.rssBytes, metrics.cpuSeconds
	event.HeapBytes, event.GCCycles = metrics.heapBytes, metrics.gcCycles
	if err := r.options.Sink(event); err != nil {
		r.failed = true
		if r.options.OnError != nil {
			r.options.OnError(err)
		}
	}
}

type resourceMetrics struct {
	rssBytes   *uint64
	cpuSeconds *float64
	heapBytes  *uint64
	gcCycles   *uint32
}
