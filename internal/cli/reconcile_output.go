package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/reconifyhq/reconify/engine"
)

type stderrWarningObserver struct{}

func (stderrWarningObserver) ObserveWarning(warning engine.Warning) {
	fmt.Fprintln(os.Stderr, "warning:", warning.Message)
}

// summaryCapture wraps a ResultWriter and captures the final Summary written by
// WriteSummary. All optional interface setup is applied to the inner writer
// before wrapping; grouped events and per-source summaries are forwarded when
// the inner writer supports them.
type summaryCapture struct {
	inner    engine.ResultWriter
	captured engine.Summary
}

func (s *summaryCapture) ObserveWarning(warning engine.Warning) {
	if observer, ok := s.inner.(engine.WarningObserver); ok {
		observer.ObserveWarning(warning)
	}
}

func (s *summaryCapture) WriteMatch(p engine.MatchedPair) error {
	return s.inner.WriteMatch(p)
}
func (s *summaryCapture) WriteAmountDiff(p engine.AmountDiffPair) error {
	return s.inner.WriteAmountDiff(p)
}
func (s *summaryCapture) WriteTimingDiff(p engine.TimingDiffPair) error {
	return s.inner.WriteTimingDiff(p)
}
func (s *summaryCapture) WriteUnmatched(tx engine.Transaction, side string) error {
	return s.inner.WriteUnmatched(tx, side)
}
func (s *summaryCapture) WriteDuplicate(g engine.DuplicateGroup) error {
	return s.inner.WriteDuplicate(g)
}
func (s *summaryCapture) WriteSummary(sum engine.Summary) error {
	s.captured = sum
	return s.inner.WriteSummary(sum)
}
func (s *summaryCapture) Flush() error { return s.inner.Flush() }

func (s *summaryCapture) SetIndexSelection(selection engine.IndexSelection) error {
	if setter, ok := s.inner.(engine.IndexSelectionSetter); ok {
		return setter.SetIndexSelection(selection)
	}
	return nil
}

// WriteSourceSummary forwards to the inner writer when it implements SourceBreakdownWriter.
func (s *summaryCapture) WriteSourceSummary(sourceName string, sum engine.Summary) error {
	if sbw, ok := s.inner.(engine.SourceBreakdownWriter); ok {
		return sbw.WriteSourceSummary(sourceName, sum)
	}
	return nil
}

// GroupedEventWriter forwarding — delegates to inner when supported.
func (s *summaryCapture) WriteGroupedMatch(p engine.GroupedMatchedPair) error {
	if gw, ok := s.inner.(engine.GroupedEventWriter); ok {
		return gw.WriteGroupedMatch(p)
	}
	return nil
}
func (s *summaryCapture) WriteGroupedAmountDiff(p engine.GroupedAmountDiffPair) error {
	if gw, ok := s.inner.(engine.GroupedEventWriter); ok {
		return gw.WriteGroupedAmountDiff(p)
	}
	return nil
}
func (s *summaryCapture) WriteGroupedTimingDiff(p engine.GroupedTimingDiffPair) error {
	if gw, ok := s.inner.(engine.GroupedEventWriter); ok {
		return gw.WriteGroupedTimingDiff(p)
	}
	return nil
}
func (s *summaryCapture) WriteAmbiguousGroup(p engine.AmbiguousGroupPair) error {
	if gw, ok := s.inner.(engine.GroupedEventWriter); ok {
		return gw.WriteAmbiguousGroup(p)
	}
	return nil
}

// ManyToManyEventWriter forwarding — delegates to inner when supported.
func (s *summaryCapture) WriteManyToManyMatch(p engine.ManyToManyMatchedPair) error {
	if mw, ok := s.inner.(engine.ManyToManyEventWriter); ok {
		return mw.WriteManyToManyMatch(p)
	}
	return nil
}
func (s *summaryCapture) WriteManyToManyAmountDiff(p engine.ManyToManyAmountDiffPair) error {
	if mw, ok := s.inner.(engine.ManyToManyEventWriter); ok {
		return mw.WriteManyToManyAmountDiff(p)
	}
	return nil
}
func (s *summaryCapture) WriteManyToManyTimingDiff(p engine.ManyToManyTimingDiffPair) error {
	if mw, ok := s.inner.(engine.ManyToManyEventWriter); ok {
		return mw.WriteManyToManyTimingDiff(p)
	}
	return nil
}

type reconcileOutput struct {
	File         *os.File
	finalPath    string
	tempPath     string
	copyToStdout bool
	directStdout bool
	closed       bool
	committed    bool
}

func openReconcileOutput(outputPath string, auditMode bool) (*reconcileOutput, error) {
	if outputPath == "-" || outputPath == "" {
		if !auditMode {
			return &reconcileOutput{File: os.Stdout, directStdout: true, committed: true}, nil
		}
		tmp, err := os.CreateTemp("", "reconify-audit-output-*")
		if err != nil {
			return nil, fmt.Errorf("create temporary audit output: %w", err)
		}
		return &reconcileOutput{File: tmp, tempPath: tmp.Name(), copyToStdout: true}, nil
	}

	dir := filepath.Dir(outputPath)
	base := filepath.Base(outputPath)
	if fi, err := os.Lstat(outputPath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("output path %q is a symlink; refusing to follow it (remove the symlink first)", outputPath)
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("output path %q exists and is not a regular file", outputPath)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect output path %q: %w", outputPath, err)
	}

	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return nil, fmt.Errorf("create temporary output file: %w", err)
	}
	return &reconcileOutput{File: tmp, finalPath: outputPath, tempPath: tmp.Name()}, nil
}

func (o *reconcileOutput) Commit() error {
	if o.committed {
		return nil
	}
	if err := o.close(); err != nil {
		return err
	}

	if o.copyToStdout {
		if err := copyFileToStdout(o.tempPath); err != nil {
			return err
		}
		o.committed = true
		if err := os.Remove(o.tempPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove temporary audit output: %w", err)
		}
		o.tempPath = ""
		return nil
	}

	if err := ensureOutputPathIsReplaceable(o.finalPath); err != nil {
		return err
	}
	if err := replaceOutputFile(o.tempPath, o.finalPath); err != nil {
		return err
	}
	o.committed = true
	return nil
}

func (o *reconcileOutput) Cleanup() {
	if o == nil || o.directStdout {
		return
	}
	if err := o.close(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: close output file: %v\n", err)
	}
	if o.tempPath != "" && !o.committed {
		if err := os.Remove(o.tempPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "warning: remove temporary output file: %v\n", err)
		}
	}
}

func (o *reconcileOutput) close() error {
	if o.closed || o.directStdout {
		return nil
	}
	o.closed = true
	if err := o.File.Close(); err != nil {
		return fmt.Errorf("close output file: %w", err)
	}
	return nil
}

func copyFileToStdout(path string) error {
	f, err := os.Open(path) // #nosec G304 -- path was created by this process for verified audit output.
	if err != nil {
		return fmt.Errorf("open verified audit output: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "warning: close verified audit output: %v\n", closeErr)
		}
	}()
	if _, err := io.Copy(os.Stdout, f); err != nil {
		return fmt.Errorf("write verified audit output to stdout: %w", err)
	}
	return nil
}

func ensureOutputPathIsReplaceable(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect output path %q: %w", path, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("output path %q is a symlink; refusing to follow it (remove the symlink first)", path)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("output path %q exists and is not a regular file", path)
	}
	return nil
}

func replaceOutputFile(tempPath, finalPath string) error {
	if err := os.Rename(tempPath, finalPath); err == nil {
		return nil
	} else if err := replaceExistingOutputFile(tempPath, finalPath, err); err != nil {
		return err
	}
	return nil
}

func replaceExistingOutputFile(tempPath, finalPath string, renameErr error) error {
	fi, err := os.Lstat(finalPath)
	if err != nil {
		return fmt.Errorf("replace output file: %w", renameErr)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("output path %q is a symlink; refusing to follow it (remove the symlink first)", finalPath)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("output path %q exists and is not a regular file", finalPath)
	}
	if err := os.Remove(finalPath); err != nil {
		return fmt.Errorf("remove existing output file: %w", err)
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		return fmt.Errorf("replace output file: %w", err)
	}
	return nil
}

// containsBatchOnlyGroupedPass reports whether any pass requires grouped batch matching.
// engine.containsPass is unexported, so the CLI keeps its own copy.
