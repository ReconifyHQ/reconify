package cli

import "fmt"

// Exit codes for the CLI. Agents and scripts should branch on these values.
const (
	// ErrCodeConfig is returned when a configuration or validation error prevents the command from running.
	ErrCodeConfig = 2
	// ErrCodeUnmatched is returned by reconcile when --fail-if-unmatched is set and unmatched rows exist.
	ErrCodeUnmatched = 3
)

// CLIError is a typed error that carries an exit code and a short machine-readable
// error code string. Commands return CLIError for expected failure conditions;
// unexpected/internal errors use plain fmt.Errorf (exit code 1).
type CLIError struct {
	Code    int    // process exit code
	ErrCode string // e.g. "config_error", "unmatched"
	Msg     string
}

func (e *CLIError) Error() string { return e.Msg }

// configErr wraps msg in a CLIError with ErrCodeConfig.
func configErr(msg string) *CLIError {
	return &CLIError{Code: ErrCodeConfig, ErrCode: "config_error", Msg: msg}
}

// configErrf wraps a formatted message in a CLIError with ErrCodeConfig.
func configErrf(format string, a ...any) *CLIError {
	return &CLIError{Code: ErrCodeConfig, ErrCode: "config_error", Msg: fmt.Sprintf(format, a...)}
}
