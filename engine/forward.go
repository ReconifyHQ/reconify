//nolint:revive // This package preserves stable compatibility names.
package engine

import (
	"github.com/reconifyhq/reconify/engine/index"
	"github.com/reconifyhq/reconify/engine/matching"
	"github.com/reconifyhq/reconify/engine/output"
	"github.com/reconifyhq/reconify/engine/parser"
	"github.com/reconifyhq/reconify/engine/reconcile"
	"github.com/reconifyhq/reconify/engine/telemetry"
)

// Parser forwarding.
var ParseEach = parser.ParseEach
var Parse = parser.Parse
var ParseCSVEach = parser.ParseCSVEach
var ParseCSV = parser.ParseCSV
var ResolveParserType = parser.ResolveParserType
var ReadInputHeaders = parser.ReadInputHeaders

// Index forwarding.
var NewMemoryIndex = index.NewMemoryIndex
var NewDiskIndex = index.NewDiskIndex

// Output forwarding.
var NewResultWriter = output.NewResultWriter
var WrapWithResultMode = output.WrapWithResultMode
var WrapWithResultModeAndWarnings = output.WrapWithResultModeAndWarnings
var WriteResultEvents = output.WriteResultEvents
var WriteResult = output.WriteResult
var SanitizeCSVField = output.SanitizeCSVField

// Matching forwarding.
var ApplyDuplicatePolicy = matching.ApplyDuplicatePolicy

// Telemetry forwarding.
var NewTelemetryRunID = telemetry.NewTelemetryRunID

// Reconciliation forwarding.
var Reconcile = reconcile.Reconcile
var ReconcileWithTelemetry = reconcile.ReconcileWithTelemetry
var ParseWithTelemetry = reconcile.ParseWithTelemetry
var ReconcileStreaming = reconcile.ReconcileStreaming
var ReconcileStreamingWithProgress = reconcile.ReconcileStreamingWithProgress
var ReconcileStreamingWithTelemetry = reconcile.ReconcileStreamingWithTelemetry
var ReconcileMultiSource = reconcile.ReconcileMultiSource
var ReconcileMultiSourceWithTelemetry = reconcile.ReconcileMultiSourceWithTelemetry
var ReconcileStreamingMultiSource = reconcile.ReconcileStreamingMultiSource
var ReconcileStreamingMultiSourceWithTelemetry = reconcile.ReconcileStreamingMultiSourceWithTelemetry
var ReconcilePartitioned = reconcile.ReconcilePartitioned
var ReconcilePartitionedWithOptions = reconcile.ReconcilePartitionedWithOptions
var ReconcilePartitionedWithTelemetry = reconcile.ReconcilePartitionedWithTelemetry
var ReconcilePartitionedWithOptionsAndTelemetry = reconcile.ReconcilePartitionedWithOptionsAndTelemetry
var ReconcilePartitionedMultiSource = reconcile.ReconcilePartitionedMultiSource
var ReconcilePartitionedMultiSourceWithOptions = reconcile.ReconcilePartitionedMultiSourceWithOptions
var ReconcilePartitionedMultiSourceWithTelemetry = reconcile.ReconcilePartitionedMultiSourceWithTelemetry
var ReconcilePartitionedMultiSourceWithOptionsAndTelemetry = reconcile.ReconcilePartitionedMultiSourceWithOptionsAndTelemetry

// Audit and partition-routing forwarding.
var BuildRunInfo = reconcile.BuildRunInfo
var VerifyAuditFiles = reconcile.VerifyAuditFiles
var PartitionKeyColumns = reconcile.PartitionKeyColumns
var PartitionDuplicateSafe = reconcile.PartitionDuplicateSafe
