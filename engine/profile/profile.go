// Package profile deterministically profiles an input file's structure —
// format, scan bounds, and per-column type inference — without requiring a
// reconify.yaml column mapping. It backs `reconify inspect` (AP-5) and is the
// factual foundation `config infer` (AP-6) builds on; it does not propose a
// column mapping or config itself.
package profile

import (
	"context"

	"github.com/reconifyhq/reconify/config"
	"github.com/reconifyhq/reconify/engine/parser"
	"github.com/reconifyhq/reconify/schemas"
)

// DefaultScanLimit is the number of rows scanned by default when Options.Full
// is false, matching the AP-5 spec ("scans 1,000 rows by default").
const DefaultScanLimit = 1000

// DefaultSampleValues is the default number of representative raw sample
// values collected per column.
const DefaultSampleValues = 3

// Options configures a profiling scan.
type Options struct {
	// Full performs an exact scan of every row instead of the default
	// bounded 1,000-row scan.
	Full bool
	// SampleValues bounds how many distinct raw sample values are collected
	// per column. 0 disables sample collection entirely.
	SampleValues int
}

// Inspect profiles filePath and returns its reconify.engine.profile.v1
// document. cfg only needs format-resolution fields (Type, Sheet, TZ); column
// mapping fields (DateCol, AmountCol, ...) are ignored because no mapping
// exists yet at inspection time.
func Inspect(ctx context.Context, filePath string, cfg config.ParserCfg, opts Options) (schemas.Profile, error) {
	format, err := parser.ResolveParserType(filePath, cfg)
	if err != nil {
		return schemas.Profile{}, err
	}

	limit := DefaultScanLimit
	if opts.Full {
		limit = 0
	}

	collector := newColumnCollector(opts.SampleValues)
	headers, truncated, err := parser.RawRowsEach(ctx, filePath, cfg, limit, func(row map[string]string, _ int) error {
		collector.observeRow(row)
		return nil
	})
	if err != nil {
		return schemas.Profile{}, err
	}

	columns := make([]schemas.ColumnProfile, 0, len(headers))
	for _, name := range headers {
		columns = append(columns, collector.buildColumnProfile(name))
	}

	return schemas.Profile{
		Schema: schemas.ProfileSchemaV1,
		File:   filePath,
		Format: format,
		Scan: schemas.ScanSummary{
			Full:        opts.Full,
			RowsScanned: collector.rowsScanned,
			Truncated:   truncated,
		},
		Columns: columns,
	}, nil
}
