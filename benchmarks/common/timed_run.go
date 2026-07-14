// Package main provides the timed subprocess runner used by benchmark scripts.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
)

func main() {
	os.Exit(run())
}

func run() int {
	timeoutSeconds := flag.Int("timeout-seconds", 0, "child timeout in seconds; zero disables the timeout")
	stdoutPath := flag.String("stdout", "", "child stdout path")
	stderrPath := flag.String("stderr", "", "child stderr path")
	monitorDir := flag.String("monitor-dir", "", "directory whose peak size should be sampled")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 || *stdoutPath == "" || *stderrPath == "" {
		fmt.Fprintln(os.Stderr, "usage: timed_run --stdout PATH --stderr PATH [--monitor-dir DIR] [--timeout-seconds N] -- COMMAND [ARGS...]")
		return 2
	}
	if err := os.MkdirAll(filepath.Dir(*stdoutPath), 0o750); err != nil {
		fmt.Fprintf(os.Stderr, "create stdout directory: %v\n", err)
		return 2
	}
	if err := os.MkdirAll(filepath.Dir(*stderrPath), 0o750); err != nil {
		fmt.Fprintf(os.Stderr, "create stderr directory: %v\n", err)
		return 2
	}

	out, err := os.Create(*stdoutPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open stdout: %v\n", err)
		return 2
	}
	defer func() { _ = out.Close() }()
	errOut, err := os.Create(*stderrPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open stderr: %v\n", err)
		return 2
	}
	defer func() { _ = errOut.Close() }()

	cmd := exec.Command(args[0], args[1:]...) // #nosec G204 -- benchmark harness intentionally runs its trusted child command.
	cmd.Stdout = out
	cmd.Stderr = errOut
	cmd.Env = append(os.Environ(), "GODEBUG=gctrace=1")

	start := time.Now()
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "start child: %v\n", err)
		fmt.Printf("TIMED_RUN\t0\t0\t0\t0\t-1\n")
		return 1
	}

	var wg sync.WaitGroup
	stopSampling := make(chan struct{})
	var peakBytes int64
	if *monitorDir != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if size := directorySize(*monitorDir); size > peakBytes {
						peakBytes = size
					}
				case <-stopSampling:
					return
				}
			}
		}()
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timedOut := false
	var runErr error
	if *timeoutSeconds > 0 {
		select {
		case runErr = <-done:
		case <-time.After(time.Duration(*timeoutSeconds) * time.Second):
			timedOut = true
			_ = cmd.Process.Kill()
			runErr = <-done
		}
	} else {
		runErr = <-done
	}
	if *monitorDir != "" {
		close(stopSampling)
		wg.Wait()
		if size := directorySize(*monitorDir); size > peakBytes {
			peakBytes = size
		}
	}

	elapsed := time.Since(start).Seconds()
	rssMB := peakRSSMB(cmd.ProcessState)
	exitCode := 0
	if runErr != nil {
		exitCode = 1
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	if timedOut {
		exitCode = 124
	}
	fmt.Printf("TIMED_RUN\t%.3f\t%.1f\t%d\t%d\t%d\n", elapsed, rssMB, peakBytes, boolInt(timedOut), exitCode)
	if timedOut {
		return 124
	}
	if runErr != nil {
		return exitCode
	}
	return 0
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func directorySize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func peakRSSMB(state *os.ProcessState) float64 {
	if state == nil {
		return 0
	}
	usage, ok := state.SysUsage().(*syscall.Rusage)
	if !ok {
		return 0
	}
	rss := float64(usage.Maxrss)
	if runtime.GOOS == "darwin" {
		rss /= 1024 * 1024
	} else {
		rss /= 1024
	}
	return rss
}
