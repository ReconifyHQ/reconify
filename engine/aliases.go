// Package engine is the stable public facade for Reconify's reconciliation engine.
//
//nolint:revive // This package preserves stable compatibility names.
package engine

import (
	"github.com/reconifyhq/reconify/engine/domain"
	"github.com/reconifyhq/reconify/engine/index"
	"github.com/reconifyhq/reconify/engine/matching"
	"github.com/reconifyhq/reconify/engine/output"
	"github.com/reconifyhq/reconify/engine/reconcile"
	"github.com/reconifyhq/reconify/engine/telemetry"
)

// Domain aliases.
type Transaction = domain.Transaction
type MatchedPair = domain.MatchedPair
type AmountDiffPair = domain.AmountDiffPair
type TimingDiffPair = domain.TimingDiffPair
type GroupedMatchedPair = domain.GroupedMatchedPair
type GroupedAmountDiffPair = domain.GroupedAmountDiffPair
type GroupedTimingDiffPair = domain.GroupedTimingDiffPair
type ManyToManyMatchedPair = domain.ManyToManyMatchedPair
type ManyToManyAmountDiffPair = domain.ManyToManyAmountDiffPair
type ManyToManyTimingDiffPair = domain.ManyToManyTimingDiffPair
type AmbiguousGroupPair = domain.AmbiguousGroupPair
type SubsetSumMatchedPair = domain.SubsetSumMatchedPair
type SubsetSumAmbiguousPair = domain.SubsetSumAmbiguousPair
type SubsetSumSkippedPair = domain.SubsetSumSkippedPair
type DuplicateGroup = domain.DuplicateGroup
type Summary = domain.Summary
type SourceSummary = domain.SourceSummary
type Result = domain.Result
type RunInfo = domain.RunInfo
type FileInfo = domain.FileInfo
type PairConfigSnap = domain.PairConfigSnap
type InferenceConfidenceSource = domain.InferenceConfidenceSource
type InferenceConfidence = domain.InferenceConfidence
type Warning = domain.Warning
type WarningObserver = domain.WarningObserver
type IndexSelection = domain.IndexSelection
type IndexFallback = domain.IndexFallback

const ResultSchemaV1 = domain.ResultSchemaV1

// Index aliases.
type RightIndex = index.RightIndex

// Matching aliases.
type ReconcileOptions = matching.ReconcileOptions

// Output aliases.
type ResultWriter = output.ResultWriter
type RunInfoSetter = output.RunInfoSetter
type IndexSelectionSetter = output.IndexSelectionSetter
type SourceBreakdownWriter = output.SourceBreakdownWriter
type GroupedEventWriter = output.GroupedEventWriter
type ManyToManyEventWriter = output.ManyToManyEventWriter
type SubsetSumEventWriter = output.SubsetSumEventWriter

// Telemetry aliases.
type TelemetryEvent = telemetry.TelemetryEvent
type TelemetryOptions = telemetry.TelemetryOptions
type TelemetrySink = telemetry.TelemetrySink
type ProgressEvent = telemetry.ProgressEvent
type ProgressFunc = telemetry.ProgressFunc

// Reconciliation aliases.
type PartitionedOptions = reconcile.PartitionedOptions
type CounterpartInput = reconcile.CounterpartInput
type CounterpartStream = reconcile.CounterpartStream
type PartitionedCounterpartInput = reconcile.PartitionedCounterpartInput
type PartitionedCounterpart = reconcile.PartitionedCounterpart
