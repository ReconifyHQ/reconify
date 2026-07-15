package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/reconifyhq/reconify/engine"
)

func openTelemetry(human bool, path string, progressEvery int, heartbeatEvery time.Duration) (engine.TelemetryOptions, func(), error) {
	if !human && path == "" {
		return engine.TelemetryOptions{}, func() {}, nil
	}
	var (
		file *os.File
		mu   sync.Mutex
	)
	if path != "" {
		var err error
		file, err = openTelemetryFile(path)
		if err != nil {
			return engine.TelemetryOptions{}, func() {}, fmt.Errorf("open --progress-out: %w", err)
		}
	}
	closeFn := func() {
		if file != nil {
			if err := file.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "warning: close telemetry output: %v\n", err)
			}
		}
	}
	return engine.TelemetryOptions{
		ProgressEvery:  progressEvery,
		HeartbeatEvery: heartbeatEvery,
		Sink: func(event engine.TelemetryEvent) error {
			mu.Lock()
			defer mu.Unlock()
			if human && event.Type == "progress" && (event.Status == "running" || event.Status == "completed") {
				elapsed := time.Duration(event.Elapsed * float64(time.Second)).Round(time.Second)
				if event.Status == "completed" {
					fmt.Fprintf(os.Stderr, "progress: %s done rows=%d elapsed=%s avg_rate=%.0f rows/s\n", event.Stage, event.Rows, elapsed, event.RowsPerSecond)
				} else {
					fmt.Fprintf(os.Stderr, "progress: %s rows=%d elapsed=%s rate=%.0f rows/s\n", event.Stage, event.Rows, elapsed, event.RowsPerSecond)
				}
			}
			if file == nil {
				return nil
			}
			return json.NewEncoder(file).Encode(event)
		},
		OnError: func(err error) {
			fmt.Fprintf(os.Stderr, "warning: telemetry output disabled: %v\n", err)
		},
	}, closeFn, nil
}

func validateProgressOutput(progressPath, resultPath string, inputPaths ...string) error {
	if progressPath == "-" || progressPath == "/dev/stdout" {
		return fmt.Errorf("--progress-out must not write to stdout")
	}
	progressAbs, err := filepath.Abs(progressPath)
	if err != nil {
		return fmt.Errorf("resolve --progress-out path: %w", err)
	}
	if info, err := os.Lstat(progressAbs); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("--progress-out path %q is a symlink; refusing to follow it", progressPath)
	}
	if progressInfo, err := os.Stat(progressAbs); err == nil {
		if stdoutInfo, statErr := os.Stdout.Stat(); statErr == nil && os.SameFile(progressInfo, stdoutInfo) {
			return fmt.Errorf("--progress-out must not write to stdout")
		}
	}
	for _, inputPath := range inputPaths {
		inputAbs, err := filepath.Abs(inputPath)
		if err != nil {
			return fmt.Errorf("resolve input path %q: %w", inputPath, err)
		}
		if progressAbs == inputAbs {
			return fmt.Errorf("--progress-out must differ from input path %q", inputPath)
		}
		progressInfo, progressErr := os.Stat(progressAbs)
		inputInfo, inputErr := os.Stat(inputAbs)
		if progressErr == nil && inputErr == nil && os.SameFile(progressInfo, inputInfo) {
			return fmt.Errorf("--progress-out must differ from input path %q", inputPath)
		}
	}
	if resultPath == "" || resultPath == "-" || resultPath == "/dev/stdout" {
		return nil
	}
	resultAbs, err := filepath.Abs(resultPath)
	if err != nil {
		return fmt.Errorf("resolve --out path: %w", err)
	}
	if progressAbs == resultAbs {
		return fmt.Errorf("--progress-out must differ from --out")
	}
	progressInfo, progressErr := os.Stat(progressAbs)
	resultInfo, resultErr := os.Stat(resultAbs)
	if progressErr == nil && resultErr == nil && os.SameFile(progressInfo, resultInfo) {
		return fmt.Errorf("--progress-out must differ from --out")
	}
	return nil
}
