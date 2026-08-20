package cli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/reconifyhq/reconify/schemas"
)

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

const (
	diagnosticCodeConfigInvalid          = "CONFIG_INVALID"
	diagnosticCodeInputUnreadable        = "INPUT_UNREADABLE"
	diagnosticCodeInputMismatch          = "INPUT_MISMATCH"
	diagnosticCodeInferenceAmbiguous     = "INFERENCE_AMBIGUOUS"
	diagnosticCodeInteractiveUnsupported = "INTERACTIVE_UNSUPPORTED"
	diagnosticCodeExecutionFailed        = "EXECUTION_FAILED"
	diagnosticCodeUnmatchedRows          = "UNMATCHED_ROWS"
	diagnosticCodeExceptionsFound        = "EXCEPTIONS_FOUND"
	diagnosticCodeInternalError          = "INTERNAL_ERROR"
)

const (
	diagnosticCategoryConfig    = "config"
	diagnosticCategoryInput     = "input"
	diagnosticCategoryInference = "inference"
	diagnosticCategoryExecution = "execution"
	diagnosticCategoryInternal  = "internal"
)

// Error is a typed error that carries an exit code and a short machine-readable
// error code string. Commands return Error for expected failure conditions;
// unexpected/internal errors use plain fmt.Errorf (exit code 1).
type Error struct {
	Code    int    // process exit code
	ErrCode string // e.g. "config_error", "unmatched"
	Msg     string
	// Diagnostic enriches the JSON error envelope without changing the legacy
	// ErrCode, Msg, or process exit code.
	Diagnostic *schemas.Diagnostic
}

func (e *Error) Error() string { return e.Msg }

// configErr wraps msg in an Error with ErrCodeConfig.
func configErr(msg string) *Error {
	return newCLIError(ErrCodeConfig, "config_error", msg,
		diagnosticCodeConfigInvalid, diagnosticCategoryConfig,
		"Fix the reported configuration or command arguments and rerun `reconify config validate`.", nil)
}

// configErrf wraps a formatted message in an Error with ErrCodeConfig.
func configErrf(format string, a ...any) *Error {
	return configErr(fmt.Sprintf(format, a...))
}

// inputErr creates an input-related diagnostic while preserving the caller's
// existing process exit code and legacy error code.
func inputErr(exitCode int, legacyCode, msg, code string) *Error {
	return newCLIError(exitCode, legacyCode, msg, code, diagnosticCategoryInput,
		"Check the input file and source mapping, then rerun `reconify config check-source`.", nil)
}

// executionErr wraps an unexpected reconciliation or output failure with an
// execution-category diagnostic. It deliberately keeps the historical
// generic legacy error code and exit code.
func executionErr(err error) *Error {
	if err == nil {
		return nil
	}
	return newCLIError(1, "error", err.Error(), diagnosticCodeExecutionFailed,
		diagnosticCategoryExecution,
		"Review the reported execution details and rerun the command after addressing the problem.", nil)
}

func newCLIError(exitCode int, legacyCode, msg, diagnosticCode, category, suggestion string, details map[string]any) *Error {
	if details == nil {
		details = make(map[string]any)
	}
	details["exit_code"] = exitCode
	details["legacy_code"] = legacyCode
	return &Error{
		Code:    exitCode,
		ErrCode: legacyCode,
		Msg:     msg,
		Diagnostic: &schemas.Diagnostic{
			Code:        diagnosticCode,
			Category:    category,
			Message:     msg,
			Details:     details,
			Suggestions: []string{suggestion},
		},
	}
}

// policyErr creates the structured diagnostic for reconciliation policy
// failures after a result has already been written.
func policyErr(exitCode int, legacyCode, msg, diagnosticCode string, details map[string]any) *Error {
	return newCLIError(exitCode, legacyCode, msg, diagnosticCode, diagnosticCategoryExecution,
		"Inspect the reconciliation summary and exception events before retrying.", details)
}

func defaultDiagnostic(exitCode int, legacyCode, msg string) schemas.Diagnostic {
	details := map[string]any{
		"exit_code":   exitCode,
		"legacy_code": legacyCode,
	}
	return schemas.Diagnostic{
		Code:        diagnosticCodeInternalError,
		Category:    diagnosticCategoryInternal,
		Message:     msg,
		Details:     details,
		Suggestions: []string{"Rerun with --verbose and report the full error if the problem persists."},
	}
}

func normalizeDiagnostic(diagnostic schemas.Diagnostic, exitCode int, legacyCode, message string) schemas.Diagnostic {
	if diagnostic.Code == "" {
		diagnostic.Code = diagnosticCodeInternalError
	}
	if diagnostic.Category == "" {
		diagnostic.Category = diagnosticCategoryInternal
	}
	if diagnostic.Message == "" {
		diagnostic.Message = message
	}
	if diagnostic.Details == nil {
		diagnostic.Details = make(map[string]any)
	}
	if _, ok := diagnostic.Details["exit_code"]; !ok {
		diagnostic.Details["exit_code"] = exitCode
	}
	if _, ok := diagnostic.Details["legacy_code"]; !ok {
		diagnostic.Details["legacy_code"] = legacyCode
	}
	if diagnostic.Suggestions == nil {
		diagnostic.Suggestions = []string{}
	}
	return diagnostic
}

// ExitCode returns the process exit code associated with an error.
func ExitCode(err error) int {
	var cliErr *Error
	if errors.As(err, &cliErr) {
		return cliErr.Code
	}
	return 1
}

// LegacyErrorCode returns the compatibility code emitted by the original JSON
// error envelope.
func LegacyErrorCode(err error) string {
	var cliErr *Error
	if errors.As(err, &cliErr) {
		return cliErr.ErrCode
	}
	return "error"
}

// DiagnosticEnvelope converts any command error into the versioned Engine
// diagnostic envelope. Untyped errors receive a conservative internal-error
// diagnostic so Cobra and unexpected failures are structured too.
func DiagnosticEnvelope(err error) schemas.DiagnosticEnvelope {
	exitCode := ExitCode(err)
	legacyCode := LegacyErrorCode(err)
	message := err.Error()
	var cliErr *Error
	if errors.As(err, &cliErr) && cliErr.Diagnostic != nil {
		diagnostic := normalizeDiagnostic(*cliErr.Diagnostic, exitCode, legacyCode, message)
		return schemas.DiagnosticEnvelope{
			Error:      message,
			Code:       legacyCode,
			OK:         false,
			Schema:     schemas.DiagnosticSchemaV1,
			Diagnostic: diagnostic,
		}
	}
	return schemas.DiagnosticEnvelope{
		Error:      message,
		Code:       legacyCode,
		OK:         false,
		Schema:     schemas.DiagnosticSchemaV1,
		Diagnostic: defaultDiagnostic(exitCode, legacyCode, message),
	}
}

// MarshalDiagnosticEnvelope serializes a command error for stderr.
func MarshalDiagnosticEnvelope(err error) ([]byte, error) {
	return json.Marshal(DiagnosticEnvelope(err))
}
