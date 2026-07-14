// Package cli provides command-line interface commands for Reconify
package cli

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/reconifyhq/reconify/config"
	"github.com/reconifyhq/reconify/engine"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configuration management commands",
		Long:  "Commands for validating and checking configuration files",
	}

	cmd.AddCommand(newConfigValidateCmd())
	cmd.AddCommand(newConfigCheckSourceCmd())
	cmd.AddCommand(newConfigInitCmd())
	cmd.AddCommand(newConfigSchemaCmd())

	return cmd
}

func newConfigValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate configuration file structure",
		Long: `Validate the structure and syntax of a reconify configuration file.
This checks that all required fields are present and have valid values.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args // avoid unused variable warning
			cfgPath := getConfigPath()
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return configErrf("failed to load config: %v", err)
			}

			if errs := cfg.Validate(); len(errs) > 0 {
				cmd.PrintErrf("❌ %s is invalid:\n", cfgPath)
				for _, e := range errs {
					cmd.PrintErrf("  - %v\n", e)
				}
				return configErr("validation failed")
			}

			cmd.PrintErrf("✅ %s is valid\n", cfgPath)
			return nil
		},
	}
}

func newConfigCheckSourceCmd() *cobra.Command {
	var sourceName string
	var filePath string

	cmd := &cobra.Command{
		Use:   "check-source",
		Short: "Check if an input file matches a source configuration",
		Long: `Check if an input file's structure matches the expected configuration for a source.
This validates that required columns exist and that sample data can be parsed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args // avoid unused variable warning
			if sourceName == "" {
				return configErr("--source is required")
			}
			if filePath == "" {
				return configErr("--file is required")
			}

			cfgPath := getConfigPath()
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return configErrf("failed to load config: %v", err)
			}

			cmd.PrintErrf("Checking source %q against file %q...\n", sourceName, filePath)

			// Verify that the source in the command exist in the config file
			source, ok := cfg.Sources[sourceName]
			if !ok {
				return configErrf("source %q not found in config", sourceName)
			}

			headers, err := engine.ReadInputHeaders(context.Background(), filePath, source.Parser)
			if err != nil {
				return configErrf("failed to read file: %v", err)
			}

			headerSet := make(map[string]bool)
			for _, header := range headers {
				headerSet[strings.ToLower(strings.TrimSpace(header))] = true
			}

			valid := true

			if !hasHeader(headerSet, source.Parser.DateCol) {
				cmd.PrintErrf("[x] date_col %q not found in input fields\n", source.Parser.DateCol)
				valid = false
			} else {
				cmd.PrintErrf("[ok] date_col %q found\n", source.Parser.DateCol)
			}

			if !hasHeader(headerSet, source.Parser.AmountCol) {
				cmd.PrintErrf("[x] amount_col %q not found in input fields\n", source.Parser.AmountCol)
				valid = false
			} else {
				cmd.PrintErrf("[ok] amount_col %q found\n", source.Parser.AmountCol)
			}

			if source.Parser.CurrencyCol != "" && !hasHeader(headerSet, source.Parser.CurrencyCol) {
				cmd.PrintErrf("[x] currency_col %q not found in input fields\n", source.Parser.CurrencyCol)
				valid = false
			} else {
				cmd.PrintErrf("[ok] currency_col %q found\n", source.Parser.CurrencyCol)
			}

			if source.Parser.NameCol != "" && !hasHeader(headerSet, source.Parser.NameCol) {
				cmd.PrintErrf("[x] name_col %q not found in input fields\n", source.Parser.NameCol)
				valid = false
			} else {
				cmd.PrintErrf("[ok] name_col %q found\n", source.Parser.NameCol)
			}

			if source.Parser.RefCol != "" && !hasHeader(headerSet, source.Parser.RefCol) {
				cmd.PrintErrf("[x] ref_col %q not found in input fields\n", source.Parser.RefCol)
				valid = false
			} else {
				cmd.PrintErrf("[ok] ref_col %q found\n", source.Parser.RefCol)
			}

			if !valid {
				return configErrf("source %q does not match file %q", sourceName, filePath)
			}

			cmd.PrintErrf("[OK] source %q matches file %q\n", sourceName, filePath)

			return nil
		},
	}

	cmd.Flags().StringVar(&sourceName, "source", "", "Source name to check")
	cmd.Flags().StringVar(&filePath, "file", "", "Input file path to validate")

	return cmd
}

func hasHeader(headers map[string]bool, name string) bool {
	return headers[strings.ToLower(strings.TrimSpace(name))]
}

// schemaOutput is the machine-readable description emitted by `reconify config schema`.
// It is hand-authored to stay stable and readable — not generated via reflection.
type schemaOutput struct {
	Version       string                 `json:"version"`
	ConfigSchema  map[string]interface{} `json:"config_schema"`
	OutputFormats map[string]interface{} `json:"output_formats"`
	NDJSONEvents  map[string]string      `json:"ndjson_event_types"`
	ExitCodes     map[string]string      `json:"exit_codes"`
}

func newConfigSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Print machine-readable schema for reconify.yaml and output formats",
		Long: `Print a JSON document describing the valid fields for reconify.yaml,
supported output formats, NDJSON event types, and exit codes.
Agents can call this once to self-bootstrap context without reading the source code.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			out := schemaOutput{
				Version: cliVersion,
				ConfigSchema: map[string]interface{}{
					"version": map[string]interface{}{
						"type": "integer", "required": true, "value": 1,
						"description": "Schema version. Must be 1.",
					},
					"timezone": map[string]interface{}{
						"type": "string", "required": false,
						"description": "IANA timezone (e.g. UTC, America/New_York). Overrides parser.tz when set.",
					},
					"index": map[string]interface{}{
						"type": "object", "required": false,
						"fields": map[string]interface{}{
							"backend": map[string]interface{}{
								"type": "string", "enum": []string{"memory", "disk", "auto", "partitioned"}, "default": "memory",
								"description": "memory (fastest), disk (lower RAM via SQLite), auto (resource-aware selection), partitioned (bounded memory for CSV one-to-one runs).",
							},
							"spill_dir": map[string]interface{}{
								"type": "string", "required": false,
								"description": "Directory for disk index temp files. Uses OS temp dir when empty.",
							},
							"auto_max_right_file_mb": map[string]interface{}{
								"type": "integer", "default": 2048,
								"description": "Right-file size threshold in MB at which the auto backend switches to disk.",
							},
							"max_memory_mb": map[string]interface{}{
								"type": "integer", "required": false,
								"description": "Maximum estimated memory budget for index selection; zero leaves it uncapped.",
							},
							"max_temp_disk_mb": map[string]interface{}{
								"type": "integer", "required": false,
								"description": "Maximum estimated temporary-disk budget for disk or partitioned indexing; zero leaves it uncapped.",
							},
						},
					},
					"sources": map[string]interface{}{
						"type": "map[name]source", "required": true,
						"description": "Map of source names to source configs.",
						"value_schema": map[string]interface{}{
							"file_pattern": map[string]interface{}{
								"type": "string", "required": true,
								"description": "Glob pattern for input files, relative to the config file directory.",
							},
							"parser": map[string]interface{}{
								"type": "object", "required": true,
								"fields": map[string]interface{}{
									"type":         map[string]interface{}{"type": "string", "enum": []string{"csv", "json", "xlsx", "auto"}, "default": "auto", "description": "Input format. auto detects from file extension."},
									"date_col":     map[string]interface{}{"type": "string", "required": true, "description": "Column name for transaction date."},
									"date_layout":  map[string]interface{}{"type": "string", "required": true, "description": "Go time layout (e.g. 2006-01-02). NOT strftime format."},
									"amount_col":   map[string]interface{}{"type": "string", "required": true, "description": "Column name for transaction amount."},
									"multiplier":   map[string]interface{}{"type": "integer", "required": true, "description": "Multiply parsed amount to convert to minor units (e.g. 100 for dollar→cents)."},
									"decimal":      map[string]interface{}{"type": "string", "default": ".", "description": "Decimal separator character."},
									"thousands":    map[string]interface{}{"type": "string", "default": "", "description": "Thousands separator to strip before parsing."},
									"currency_col": map[string]interface{}{"type": "string", "required": false, "description": "Column name for currency code."},
									"name_col":     map[string]interface{}{"type": "string", "required": false, "description": "Column name for counterpart name. Required when pair name_mode is tokens."},
									"ref_col":      map[string]interface{}{"type": "string", "required": false, "description": "Column name for transaction reference/ID used in matching."},
									"group_col":    map[string]interface{}{"type": "string", "required": false, "description": "Column for duplicate grouping key. Falls back to ref_col when absent."},
									"tz":           map[string]interface{}{"type": "string", "required": false, "description": "IANA timezone for dates without timezone info. Overridden by top-level timezone."},
									"sheet":        map[string]interface{}{"type": "string", "required": false, "description": "Sheet name for xlsx files. Uses first sheet when empty."},
									"skip_raw":     map[string]interface{}{"type": "boolean", "default": false, "description": "Skip allocating the Raw field map. Reduces memory for large files."},
								},
							},
						},
					},
					"pairs": map[string]interface{}{
						"type": "map[name]pair", "required": true,
						"description": "Map of pair names to reconciliation pair configs.",
						"value_schema": map[string]interface{}{
							"left":                   map[string]interface{}{"type": "string", "required": true, "description": "Name of the left source."},
							"right":                  map[string]interface{}{"type": "string", "required": false, "description": "Name of a single right source. Use rights for multiple counterparts."},
							"rights":                 map[string]interface{}{"type": "array[string]", "required": false, "description": "Names of multiple right sources for 1-N reconciliation. Mutually exclusive with right."},
							"date_window":            map[string]interface{}{"type": "string", "default": "0d", "description": "Allowed date difference between matched transactions (e.g. 1d, 2d). 0d requires exact date match."},
							"amount_tolerance_minor": map[string]interface{}{"type": "integer", "default": 0, "description": "Allowed amount difference in minor units before reporting amount_diff."},
							"name_mode":              map[string]interface{}{"type": "string", "enum": []string{"none", "tokens"}, "required": false, "description": "none = match by reference only; tokens = also match by tokenized counterpart name. Optional when passes is explicitly set (name_mode=tokens is rejected when passes is set)."},
							"name_match_threshold":   map[string]interface{}{"type": "float", "default": 0.5, "description": "Minimum token match ratio (0–1) when name_mode is tokens."},
							"passes": map[string]interface{}{
								"type": "array[pass]", "required": false,
								"description": "Explicit reconciliation pass pipeline. Inferred from name_mode when absent.",
								"item_schema": map[string]interface{}{
									"type": map[string]interface{}{"type": "string", "enum": []string{"reference_one_to_one", "name_tokens_one_to_one", "one_to_many", "many_to_many"}, "required": true},
								},
							},
						},
					},
				},
				OutputFormats: map[string]interface{}{
					"reconcile": map[string]interface{}{
						"formats":                  []string{"json", "json-stream", "ndjson", "csv", "table"},
						"default":                  "json",
						"streaming_formats":        []string{"ndjson", "csv", "json-stream"},
						"audit_compatible":         []string{"json", "json-stream", "ndjson"},
						"deterministic_compatible": []string{"json"},
					},
					"parse": map[string]interface{}{
						"formats":           []string{"ndjson", "csv", "table", "json"},
						"default":           "ndjson",
						"streaming_formats": []string{"ndjson", "csv"},
					},
				},
				// Event names match engine/format.go ndjsonWriter exactly.
				// Each line is {"type":"<name>","data":{...}}.
				NDJSONEvents: map[string]string{
					"run_info":                 "Run provenance: tool version, file hashes, timestamps. Only emitted when --audit is set. Always the first line. Payload: RunInfo.",
					"match":                    "Left+right pair that reconciled cleanly. Payload: {left: Transaction, right: Transaction}.",
					"amount_diff":              "Reference matched but amount differs beyond amount_tolerance_minor. Payload: {left, right, diff_minor}.",
					"timing_diff":              "Reference+amount matched but date is outside date_window. Payload: {left, right, days_diff}.",
					"unmatched_left":           "Left transaction with no match on the right side. Payload: Transaction.",
					"unmatched_right":          "Right transaction with no match on the left side. Payload: Transaction.",
					"grouped_match":            "One left matched to N rights (one_to_many pass). Payload: {left, rights: []Transaction}.",
					"grouped_amount_diff":      "Grouped match where sum of right amounts differs beyond tolerance. Payload: {left, rights, diff_minor}.",
					"grouped_timing_diff":      "Grouped match where at least one right date is outside date_window. Payload: {left, rights, days_diff}.",
					"many_to_many_match":       "M left rows matched to N right rows by shared group key. Payload: {lefts: []Transaction, rights: []Transaction}.",
					"many_to_many_amount_diff": "N:M grouped match where summed left and right amounts differ beyond tolerance. Payload: {lefts, rights, diff_minor}.",
					"many_to_many_timing_diff": "N:M grouped match where at least one cross-side date is outside date_window. Payload: {lefts, rights, days_diff}.",
					"ambiguous_group":          "Multiple left rows share the same reference; manual review required. Payload: {reference, left_rows, rights}.",
					"duplicate":                "Transactions within the same source sharing the same reference. Payload: {source, reference, transactions}.",
					"source_summary":           "Per-counterpart summary for 1-N runs. One per counterpart, before final summary. Payload: {source: string, summary: Summary}.",
					"summary":                  "Aggregate counts and match rate. Always the last line. Payload: Summary.",
				},
				ExitCodes: map[string]string{
					"0": "Success.",
					"1": "Unexpected or internal error.",
					"2": "Config or validation error (bad YAML, missing pair/source, column not found).",
					"3": "Reconcile completed with unmatched rows. Only returned when --fail-if-unmatched is set.",
				},
			}

			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(out)
		},
	}
}
