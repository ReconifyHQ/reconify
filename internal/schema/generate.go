// Package schema generates the published Reconify Engine result schema.
package schema

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
	domain "github.com/reconifyhq/reconify/engine/domain"
)

const draft202012 = "https://json-schema.org/draft/2020-12/schema"
const resultSchemaID = "urn:reconify:engine:result:v1"

type eventDefinition struct {
	typ      string
	dataType reflect.Type
}

// GenerateResultSchema constructs the versioned result schema from the
// structured result and event payload types used by the Engine.
func GenerateResultSchema() (*jsonschema.Schema, error) {
	result, err := jsonschema.For[domain.Result](nil)
	if err != nil {
		return nil, fmt.Errorf("reflect result schema: %w", err)
	}

	result.ID = ""
	result.Schema = ""
	result.Defs = ensureDefs(result.Defs)
	resultDef := result
	if result.Ref != "" {
		resultDef = result.Defs["Result"]
		if resultDef == nil {
			return nil, fmt.Errorf("reflected result schema has no Result definition")
		}
	}
	if property := resultDef.Properties["schema"]; property != nil {
		property.Const = jsonschema.Ptr[any](domain.ResultSchemaV1)
	} else {
		return nil, fmt.Errorf("reflected Result schema has no schema property")
	}

	events := []eventDefinition{
		{typ: "run_info", dataType: reflect.TypeOf(domain.RunInfo{})},
		{typ: "index_selection", dataType: reflect.TypeOf(domain.IndexSelection{})},
		{typ: "match", dataType: reflect.TypeOf(domain.MatchedPair{})},
		{typ: "amount_diff", dataType: reflect.TypeOf(domain.AmountDiffPair{})},
		{typ: "timing_diff", dataType: reflect.TypeOf(domain.TimingDiffPair{})},
		{typ: "unmatched_left", dataType: reflect.TypeOf(domain.Transaction{})},
		{typ: "unmatched_right", dataType: reflect.TypeOf(domain.Transaction{})},
		{typ: "grouped_match", dataType: reflect.TypeOf(domain.GroupedMatchedPair{})},
		{typ: "grouped_amount_diff", dataType: reflect.TypeOf(domain.GroupedAmountDiffPair{})},
		{typ: "grouped_timing_diff", dataType: reflect.TypeOf(domain.GroupedTimingDiffPair{})},
		{typ: "many_to_many_match", dataType: reflect.TypeOf(domain.ManyToManyMatchedPair{})},
		{typ: "many_to_many_amount_diff", dataType: reflect.TypeOf(domain.ManyToManyAmountDiffPair{})},
		{typ: "many_to_many_timing_diff", dataType: reflect.TypeOf(domain.ManyToManyTimingDiffPair{})},
		{typ: "ambiguous_group", dataType: reflect.TypeOf(domain.AmbiguousGroupPair{})},
		{typ: "duplicate", dataType: reflect.TypeOf(domain.DuplicateGroup{})},
		{typ: "source_summary", dataType: reflect.TypeOf(domain.SourceSummary{})},
		{typ: "summary", dataType: reflect.TypeOf(domain.Summary{})},
		{typ: "financial_effect_match", dataType: reflect.TypeOf(domain.FinancialEffectFinding{})},
		{typ: "financial_effect_diff", dataType: reflect.TypeOf(domain.FinancialEffectFinding{})},
		{typ: "financial_unchecked", dataType: reflect.TypeOf(domain.FinancialEffectFinding{})},
		{typ: "settlement_match", dataType: reflect.TypeOf(domain.SettlementFinding{})},
		{typ: "settlement_diff", dataType: reflect.TypeOf(domain.SettlementFinding{})},
	}

	branches := make([]*jsonschema.Schema, 0, len(events))
	for _, event := range events {
		payload, err := jsonschema.ForType(event.dataType, nil)
		if err != nil {
			return nil, fmt.Errorf("reflect %s payload schema: %w", event.typ, err)
		}
		mergeDefs(result.Defs, payload.Defs)
		data := payload
		if payload.Ref != "" {
			data = &jsonschema.Schema{Ref: payload.Ref}
		}
		branches = append(branches, &jsonschema.Schema{
			Type:     "object",
			Required: []string{"schema", "type", "data"},
			Properties: map[string]*jsonschema.Schema{
				"schema": constString(domain.ResultSchemaV1),
				"type":   constString(event.typ),
				"data":   data,
			},
			AdditionalProperties: noAdditionalProperties(),
		})
	}

	resultBranch := result.CloneSchemas()
	if result.Ref != "" {
		resultBranch = &jsonschema.Schema{Ref: result.Ref}
	}

	return &jsonschema.Schema{
		ID:          resultSchemaID,
		Schema:      draft202012,
		Title:       "Reconify Engine reconciliation result v1",
		Description: "Versioned JSON, JSON-stream, and NDJSON reconciliation result payloads.",
		OneOf: []*jsonschema.Schema{
			resultBranch,
			{
				Type:     "object",
				Required: []string{"schema", "type", "data"},
				Properties: map[string]*jsonschema.Schema{
					"schema": constString(domain.ResultSchemaV1),
					"type":   {Type: "string"},
					"data":   {},
				},
				OneOf:                branches,
				AdditionalProperties: noAdditionalProperties(),
			},
		},
		Defs: result.Defs,
	}, nil
}

// GenerateResultSchemaJSON returns the deterministic, pretty-printed schema
// document committed under schemas/.
func GenerateResultSchemaJSON() ([]byte, error) {
	s, err := GenerateResultSchema()
	if err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal result schema: %w", err)
	}
	return append(b, '\n'), nil
}

func constString(value string) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Const: jsonschema.Ptr[any](value)}
}

func noAdditionalProperties() *jsonschema.Schema {
	return &jsonschema.Schema{Not: &jsonschema.Schema{}}
}

func ensureDefs(defs map[string]*jsonschema.Schema) map[string]*jsonschema.Schema {
	if defs == nil {
		return make(map[string]*jsonschema.Schema)
	}
	return defs
}

func mergeDefs(dst, src map[string]*jsonschema.Schema) {
	for name, definition := range src {
		if _, exists := dst[name]; !exists {
			dst[name] = definition
		}
	}
}
