package cli

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/reconifyhq/reconify/schemas"
)

func TestDiagnosticEnvelopeCatalog(t *testing.T) {
	cases := []struct {
		name           string
		err            error
		wantExit       int
		wantLegacyCode string
		wantCode       string
		wantCategory   string
		wantDetails    map[string]any
	}{
		{
			name:           "config",
			err:            configErr("bad config"),
			wantExit:       ErrCodeConfig,
			wantLegacyCode: "config_error",
			wantCode:       diagnosticCodeConfigInvalid,
			wantCategory:   diagnosticCategoryConfig,
			wantDetails:    map[string]any{"exit_code": float64(ErrCodeConfig), "legacy_code": "config_error"},
		},
		{
			name:           "input mismatch",
			err:            inputErr(ErrCodeConfig, "config_error", "source mismatch", diagnosticCodeInputMismatch),
			wantExit:       ErrCodeConfig,
			wantLegacyCode: "config_error",
			wantCode:       diagnosticCodeInputMismatch,
			wantCategory:   diagnosticCategoryInput,
			wantDetails:    map[string]any{"exit_code": float64(ErrCodeConfig), "legacy_code": "config_error"},
		},
		{
			name: "exceptions",
			err: policyErr(ErrCodeExceptions, "exceptions", "exceptions", diagnosticCodeExceptionsFound, map[string]any{
				"amount_diff_count": 2,
				"timing_diff_count": 1,
				"unmatched_left":    3,
				"unmatched_right":   4,
			}),
			wantExit:       ErrCodeExceptions,
			wantLegacyCode: "exceptions",
			wantCode:       diagnosticCodeExceptionsFound,
			wantCategory:   diagnosticCategoryExecution,
			wantDetails: map[string]any{
				"amount_diff_count": float64(2),
				"timing_diff_count": float64(1),
				"unmatched_left":    float64(3),
				"unmatched_right":   float64(4),
				"exit_code":         float64(ErrCodeExceptions),
				"legacy_code":       "exceptions",
			},
		},
		{
			name:           "unmatched",
			err:            policyErr(ErrCodeUnmatched, "unmatched", "unmatched", diagnosticCodeUnmatchedRows, map[string]any{"unmatched_left": 2, "unmatched_right": 1}),
			wantExit:       ErrCodeUnmatched,
			wantLegacyCode: "unmatched",
			wantCode:       diagnosticCodeUnmatchedRows,
			wantCategory:   diagnosticCategoryExecution,
			wantDetails:    map[string]any{"unmatched_left": float64(2), "unmatched_right": float64(1), "exit_code": float64(ErrCodeUnmatched), "legacy_code": "unmatched"},
		},
		{
			name:           "generic internal",
			err:            errors.New("unexpected failure"),
			wantExit:       1,
			wantLegacyCode: "error",
			wantCode:       diagnosticCodeInternalError,
			wantCategory:   diagnosticCategoryInternal,
			wantDetails:    map[string]any{"exit_code": float64(1), "legacy_code": "error"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := MarshalDiagnosticEnvelope(tc.err)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Error      string             `json:"error"`
				Code       string             `json:"code"`
				OK         bool               `json:"ok"`
				Schema     string             `json:"schema"`
				Diagnostic schemas.Diagnostic `json:"diagnostic"`
			}
			if err := json.Unmarshal(payload, &got); err != nil {
				t.Fatalf("decode envelope: %v", err)
			}
			if ExitCode(tc.err) != tc.wantExit || LegacyErrorCode(tc.err) != tc.wantLegacyCode {
				t.Fatalf("compatibility fields = (%d, %q), want (%d, %q)", ExitCode(tc.err), LegacyErrorCode(tc.err), tc.wantExit, tc.wantLegacyCode)
			}
			if got.OK || got.Schema != schemas.DiagnosticSchemaV1 || got.Code != tc.wantLegacyCode || got.Error != tc.err.Error() {
				t.Fatalf("envelope compatibility fields = %+v", got)
			}
			if got.Diagnostic.Code != tc.wantCode || got.Diagnostic.Category != tc.wantCategory || got.Diagnostic.Message != tc.err.Error() {
				t.Fatalf("diagnostic = %+v", got.Diagnostic)
			}
			if !jsonEqual(got.Diagnostic.Details, tc.wantDetails) {
				t.Fatalf("details = %#v, want %#v", got.Diagnostic.Details, tc.wantDetails)
			}
			if len(got.Diagnostic.Suggestions) != 1 || got.Diagnostic.Suggestions[0] == "" {
				t.Fatalf("suggestions = %#v, want one actionable suggestion", got.Diagnostic.Suggestions)
			}
		})
	}
}

func jsonEqual(left, right any) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}
