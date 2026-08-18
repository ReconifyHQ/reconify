package cli

import "fmt"

// Exit codes for the CLI. Agents and scripts should branch on these values.
const (
	// ErrCodeConfig is returned when a configuration or validation error prevents the command from running.
	ErrCodeConfig = 2
	// ErrCodeUnmatched is returned by reconcile when --fail-if-unmatched is set and unmatched rows exist.
	ErrCodeUnmatched = 3
	// ErrCodeExceptions is returned by reconcile when --fail-if-exceptions is set and any
	// amount_diff, timing_diff, or unmatched event was emitted. It is a superset of
	// --fail-if-unmatched and takes precedence over ErrCodeUnmatched when both flags are set.
	ErrCodeExceptions = 4
)

// Error is a typed error that carries an exit code and a short machine-readable
// error code string. Commands return Error for expected failure conditions;
// unexpected/internal errors use plain fmt.Errorf (exit code 1).
type Error struct {
	Code    int    // process exit code
	ErrCode string // e.g. "config_error", "unmatched"
	Msg     string
}

func (e *Error) Error() string { return e.Msg }

// configErr wraps msg in an Error with ErrCodeConfig.
func configErr(msg string) *Error {
	return &Error{Code: ErrCodeConfig, ErrCode: "config_error", Msg: msg}
}

// configErrf wraps a formatted message in an Error with ErrCodeConfig.
func configErrf(format string, a ...any) *Error {
	return &Error{Code: ErrCodeConfig, ErrCode: "config_error", Msg: fmt.Sprintf(format, a...)}
}
