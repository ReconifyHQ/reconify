// Package inference builds deterministic reconify.yaml proposals from two files.
package inference

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/reconifyhq/reconify/config"
	"github.com/reconifyhq/reconify/engine/parser"
	"github.com/reconifyhq/reconify/engine/profile"
	"github.com/reconifyhq/reconify/engine/sample"
	"github.com/reconifyhq/reconify/schemas"
)

const sampleLimit = profile.DefaultScanLimit

var aliases = map[string][]string{
	"date":      {"date", "posted", "created"},
	"amount":    {"amount", "value", "total"},
	"reference": {"reference", "ref", "id", "transaction_id"},
}

// Infer returns a proposal for exactly two input files.
func Infer(ctx context.Context, leftPath, rightPath string) (schemas.ConfigProposal, error) {
	left, leftCfg, err := inferSource(ctx, "left", leftPath)
	if err != nil {
		return schemas.ConfigProposal{}, err
	}
	right, rightCfg, err := inferSource(ctx, "right", rightPath)
	if err != nil {
		return schemas.ConfigProposal{}, err
	}
	cfg := &config.Config{Version: 1, Sources: map[string]config.Source{
		"left":  {FilePattern: left.File, Parser: leftCfg},
		"right": {FilePattern: right.File, Parser: rightCfg},
	}, Pairs: map[string]config.Pair{
		"left_to_right": {Left: "left", Right: "right", DateWindow: "1d", AmountToleranceMinor: 0, NameMode: "none"},
	}}
	yamlData, err := yaml.Marshal(cfg)
	if err != nil {
		return schemas.ConfigProposal{}, fmt.Errorf("marshal proposal: %w", err)
	}
	proposal := schemas.ConfigProposal{Schema: schemas.ConfigProposalSchemaV1, Status: "ready", Sources: []schemas.InferredSource{left, right}, ProposedYAML: string(yamlData)}
	for _, source := range proposal.Sources {
		if source.Validation.SuccessfulRows < 100 {
			proposal.Status = "needs_input"
			proposal.Reasons = append(proposal.Reasons, fmt.Sprintf("%s has only %d successfully parsed sample rows (need 100)", source.Name, source.Validation.SuccessfulRows))
		}
		for _, mapping := range source.Mappings {
			if !mapping.Ready {
				proposal.Status = "needs_input"
				proposal.Reasons = append(proposal.Reasons, fmt.Sprintf("%s %s mapping is not confident enough", source.Name, mapping.Role))
			}
		}
	}
	return proposal, nil
}

func inferSource(ctx context.Context, name, filePath string) (schemas.InferredSource, config.ParserCfg, error) {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return schemas.InferredSource{}, config.ParserCfg{}, fmt.Errorf("resolve %q: %w", filePath, err)
	}
	p, err := profile.Inspect(ctx, absPath, config.ParserCfg{}, profile.Options{SampleValues: 0})
	if err != nil {
		return schemas.InferredSource{}, config.ParserCfg{}, err
	}
	if len(p.Columns) == 0 {
		return schemas.InferredSource{}, config.ParserCfg{}, fmt.Errorf("%q has no columns", filePath)
	}
	refStats, err := collectReferenceStats(ctx, absPath)
	if err != nil {
		return schemas.InferredSource{}, config.ParserCfg{}, err
	}
	date := rankTyped("date", p.Columns)
	amount := rankTyped("amount", p.Columns)
	reference := rankReference(p.Columns, refStats)
	cfg := config.ParserCfg{Type: "auto", DateCol: date.Column, DateLayout: dateLayout(p.Columns, date.Column), AmountCol: amount.Column, Multiplier: 100, RefCol: reference.Column}
	if format := amountFormat(p.Columns, amount.Column); format != nil {
		cfg.Decimal, cfg.Thousands = format.Decimal, format.Thousands
	}
	validation, err := sample.Validate(ctx, absPath, cfg, sampleLimit)
	if err != nil {
		return schemas.InferredSource{}, config.ParserCfg{}, err
	}
	date = setReadiness(date, validation.SuccessfulRows)
	amount = setReadiness(amount, validation.SuccessfulRows)
	reference = setReadiness(reference, validation.SuccessfulRows)
	return schemas.InferredSource{Name: name, File: absPath, Format: p.Format, Validation: schemas.InferenceValidation{RowsScanned: validation.RowsScanned, SuccessfulRows: validation.SuccessfulRows, Truncated: validation.Truncated}, Mappings: []schemas.InferredRole{date, amount, reference}}, cfg, nil
}

type refStats struct{ nonEmpty, distinct, rows int }

func collectReferenceStats(ctx context.Context, path string) (map[string]refStats, error) {
	values := map[string]map[string]struct{}{}
	nonEmpty := map[string]int{}
	rows := 0
	_, _, err := parser.RawRowsEach(ctx, path, config.ParserCfg{}, sampleLimit, func(row map[string]string, _ int) error {
		rows++
		for column, raw := range row {
			key := strings.ToLower(strings.TrimSpace(column))
			if strings.TrimSpace(raw) == "" {
				continue
			}
			nonEmpty[key]++
			if values[key] == nil {
				values[key] = map[string]struct{}{}
			}
			values[key][raw] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result := map[string]refStats{}
	for key, count := range nonEmpty {
		result[key] = refStats{nonEmpty: count, distinct: len(values[key]), rows: rows}
	}
	return result, nil
}

func rankTyped(role string, columns []schemas.ColumnProfile) schemas.InferredRole {
	candidates := make([]schemas.MappingCandidate, 0, len(columns))
	for _, col := range columns {
		confidence := candidateConfidence(col, role) * 0.9
		if isAlias(role, col.Name) {
			confidence += 0.1
		}
		candidates = append(candidates, schemas.MappingCandidate{Column: col.Name, Confidence: round(confidence)})
	}
	return buildRole(role, candidates)
}

func rankReference(columns []schemas.ColumnProfile, stats map[string]refStats) schemas.InferredRole {
	candidates := make([]schemas.MappingCandidate, 0, len(columns))
	for _, col := range columns {
		s := stats[strings.ToLower(strings.TrimSpace(col.Name))]
		coverage, unique := 0.0, 0.0
		if s.rows > 0 {
			coverage = float64(s.nonEmpty) / float64(s.rows)
		}
		if s.nonEmpty > 0 {
			unique = float64(s.distinct) / float64(s.nonEmpty)
		}
		confidence := 0.2*coverage + 0.1*unique
		if isAlias("reference", col.Name) {
			confidence += 0.7
		}
		candidates = append(candidates, schemas.MappingCandidate{Column: col.Name, Confidence: round(confidence)})
	}
	return buildRole("reference", candidates)
}

func buildRole(role string, candidates []schemas.MappingCandidate) schemas.InferredRole {
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Confidence > candidates[j].Confidence })
	selected := candidates[0]
	lead := selected.Confidence
	if len(candidates) > 1 {
		lead = round(selected.Confidence - candidates[1].Confidence)
	}
	return schemas.InferredRole{Role: role, Column: selected.Column, Confidence: selected.Confidence, Lead: lead, Alternatives: candidates}
}

func setReadiness(role schemas.InferredRole, successful int) schemas.InferredRole {
	role.Ready = role.Confidence >= 0.9 && role.Lead >= 0.1 && successful >= 100
	if role.Confidence < 0.9 {
		role.Reasons = append(role.Reasons, "confidence is below 0.90")
	}
	if role.Lead < 0.1 {
		role.Reasons = append(role.Reasons, "confidence lead is below 0.10")
	}
	if successful < 100 {
		role.Reasons = append(role.Reasons, "fewer than 100 successfully parsed sample rows")
	}
	return role
}

func candidateConfidence(column schemas.ColumnProfile, typ string) float64 {
	for _, candidate := range column.Candidates {
		if candidate.Type == typ {
			return candidate.Confidence
		}
	}
	return 0
}
func isAlias(role, column string) bool {
	for _, alias := range aliases[role] {
		if strings.EqualFold(strings.TrimSpace(column), alias) {
			return true
		}
	}
	return false
}
func dateLayout(columns []schemas.ColumnProfile, name string) string {
	for _, col := range columns {
		if col.Name == name && col.DateLayout != "" {
			return col.DateLayout
		}
	}
	return "2006-01-02"
}
func amountFormat(columns []schemas.ColumnProfile, name string) *schemas.AmountFormat {
	for _, col := range columns {
		if col.Name == name {
			return col.AmountFormat
		}
	}
	return nil
}
func round(value float64) float64 { return math.Round(value*100) / 100 }
