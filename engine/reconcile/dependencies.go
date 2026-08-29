//nolint:revive // This package preserves stable compatibility names.
//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package reconcile

import (
	"github.com/reconifyhq/reconify/engine/domain"
	"github.com/reconifyhq/reconify/engine/index"
	"github.com/reconifyhq/reconify/engine/matching"
	"github.com/reconifyhq/reconify/engine/output"
	"github.com/reconifyhq/reconify/engine/parser"
	"github.com/reconifyhq/reconify/engine/telemetry"
)

type RightIndex = index.RightIndex
type ResultWriter = output.ResultWriter
type RunInfoSetter = output.RunInfoSetter
type IndexSelectionSetter = output.IndexSelectionSetter
type SourceBreakdownWriter = output.SourceBreakdownWriter
type GroupedEventWriter = output.GroupedEventWriter
type ManyToManyEventWriter = output.ManyToManyEventWriter
type SubsetSumEventWriter = output.SubsetSumEventWriter
type FinancialEventWriter = output.FinancialEventWriter
type ReconcileOptions = matching.ReconcileOptions
type TelemetryEvent = telemetry.TelemetryEvent
type TelemetryOptions = telemetry.TelemetryOptions
type TelemetrySink = telemetry.TelemetrySink
type ProgressEvent = telemetry.ProgressEvent
type ProgressFunc = telemetry.ProgressFunc

var ParseEach = parser.ParseEach
var Parse = parser.Parse
var NewMemoryIndex = index.NewMemoryIndex
var NewResultWriter = output.NewResultWriter
var WriteResultEvents = output.WriteResultEvents
var WriteResult = output.WriteResult
var WrapWithResultMode = output.WrapWithResultMode
var WrapWithResultModeAndWarnings = output.WrapWithResultModeAndWarnings

func observeWarning(target any, reporter *telemetry.Reporter, warning domain.Warning) {
	if observer, ok := target.(domain.WarningObserver); ok {
		observer.ObserveWarning(warning)
	}
	reporter.Warning(warning)
}
