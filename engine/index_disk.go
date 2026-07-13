package engine

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// diskIndex stores right-side rows in a temporary on-disk SQLite database.
//
// Tradeoff vs memoryIndex:
//   - lower RAM usage at large file sizes
//   - slower point lookups (ref -> candidates)
//
// It is intended for very large datasets where the in-memory index does not fit.
type diskIndex struct {
	db        *sql.DB
	insertTx  *sql.Tx
	insertSQL *sql.Stmt
	insertN   int
	getSQL    *sql.Stmt
	markSQL   *sql.Stmt
	tmpDir    string
}

const diskIndexBatchSize = 10_000

// NewDiskIndex creates a disk-backed RightIndex.
//
// If spillDir is empty, os.TempDir() is used. A private temporary directory is
// created and removed on Close().
func NewDiskIndex(spillDir string) (RightIndex, error) {
	baseDir := spillDir
	if baseDir == "" {
		baseDir = os.TempDir()
	}
	tmpDir, err := os.MkdirTemp(baseDir, "reconify-index-*")
	if err != nil {
		return nil, fmt.Errorf("create spill dir: %w", err)
	}
	dbPath := filepath.Join(tmpDir, "right-index.sqlite")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("open sqlite index: %w", err)
	}
	db.SetMaxOpenConns(1)

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA temp_store=MEMORY;",
		"PRAGMA busy_timeout=5000;",
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			_ = os.RemoveAll(tmpDir)
			return nil, fmt.Errorf("sqlite pragma failed (%s): %w", pragma, err)
		}
	}

	if _, err := db.Exec(`
		CREATE TABLE tx (
			ref       TEXT,
			id        TEXT,
			date_unix INTEGER,
			amount    INTEGER,
			currency  TEXT,
			name      TEXT,
			source    TEXT,
			used      INTEGER NOT NULL DEFAULT 0
		);
	`); err != nil {
		_ = db.Close()
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("create tx table: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX idx_tx_ref_used ON tx(ref, used);`); err != nil {
		_ = db.Close()
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("create tx index: %w", err)
	}

	getSQL, err := db.Prepare(`
		SELECT rowid, id, date_unix, amount, currency, name, source
		FROM tx
		WHERE ref = ? AND used = 0;
	`)
	if err != nil {
		_ = db.Close()
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("prepare get: %w", err)
	}
	markSQL, err := db.Prepare(`UPDATE tx SET used = 1 WHERE rowid = ? AND used = 0;`)
	if err != nil {
		_ = getSQL.Close()
		_ = db.Close()
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("prepare mark-used: %w", err)
	}

	return &diskIndex{
		db:      db,
		getSQL:  getSQL,
		markSQL: markSQL,
		tmpDir:  tmpDir,
	}, nil
}

func (d *diskIndex) Add(tx Transaction) error {
	if d.insertTx == nil {
		insertTx, err := d.db.Begin()
		if err != nil {
			return fmt.Errorf("begin disk index insert transaction: %w", err)
		}
		insertSQL, err := insertTx.Prepare(`
			INSERT INTO tx (ref, id, date_unix, amount, currency, name, source, used)
			VALUES (?, ?, ?, ?, ?, ?, ?, 0);
		`)
		if err != nil {
			_ = insertTx.Rollback()
			return fmt.Errorf("prepare insert: %w", err)
		}
		d.insertTx = insertTx
		d.insertSQL = insertSQL
		d.insertN = 0
	}
	if _, err := d.insertSQL.Exec(
		tx.Reference,
		tx.ID,
		tx.Date.UnixNano(),
		tx.Amount,
		tx.Currency,
		tx.Name,
		tx.Source,
	); err != nil {
		return fmt.Errorf("disk index insert: %w", err)
	}
	d.insertN++
	if d.insertN >= diskIndexBatchSize {
		if err := d.flushInserts(); err != nil {
			return err
		}
	}
	return nil
}

func (d *diskIndex) flushInserts() error {
	if d.insertTx == nil {
		return nil
	}
	if err := d.insertSQL.Close(); err != nil {
		_ = d.insertTx.Rollback()
		d.insertTx = nil
		d.insertSQL = nil
		return fmt.Errorf("close insert statement: %w", err)
	}
	if err := d.insertTx.Commit(); err != nil {
		d.insertTx = nil
		d.insertSQL = nil
		return fmt.Errorf("commit disk index batch: %w", err)
	}
	d.insertTx = nil
	d.insertSQL = nil
	d.insertN = 0
	return nil
}

func (d *diskIndex) Get(ref string) (buckets []*bucket, err error) {
	if err := d.flushInserts(); err != nil {
		return nil, err
	}
	rows, err := d.getSQL.Query(ref)
	if err != nil {
		return nil, fmt.Errorf("disk index get query: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("disk index get close rows: %w", closeErr)
		}
	}()

	buckets = make([]*bucket, 0, 1)
	for rows.Next() {
		b := &bucket{}
		if err := rows.Scan(&b.rowID, &b.id, &b.dateUnix, &b.amount, &b.currency, &b.name, &b.source); err != nil {
			return nil, fmt.Errorf("disk index get scan: %w", err)
		}
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("disk index get rows: %w", err)
	}
	return buckets, nil
}

func (d *diskIndex) MarkUsed(b *bucket) error {
	if b == nil || b.used {
		return nil
	}
	if b.rowID == 0 {
		return fmt.Errorf("disk index bucket missing rowid")
	}
	if _, err := d.markSQL.Exec(b.rowID); err != nil {
		return fmt.Errorf("disk index mark used: %w", err)
	}
	b.used = true
	return nil
}

func (d *diskIndex) IterateUnused(fn func(tx Transaction) error) (err error) {
	if err := d.flushInserts(); err != nil {
		return err
	}
	rows, err := d.db.Query(`
		SELECT ref, id, date_unix, amount, currency, name, source
		FROM tx
		WHERE used = 0;
	`)
	if err != nil {
		return fmt.Errorf("iterate unused: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("iterate unused close rows: %w", closeErr)
		}
	}()

	for rows.Next() {
		var (
			ref      string
			id       string
			dateUnix int64
			amount   int64
			currency string
			name     string
			source   string
		)
		if err := rows.Scan(&ref, &id, &dateUnix, &amount, &currency, &name, &source); err != nil {
			return fmt.Errorf("iterate unused scan: %w", err)
		}
		if err := fn(Transaction{
			ID:        id,
			Date:      time.Unix(0, dateUnix).UTC(),
			Amount:    amount,
			Currency:  currency,
			Reference: ref,
			Name:      name,
			Source:    source,
		}); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate unused rows: %w", err)
	}
	return nil
}

func (d *diskIndex) Close() error {
	var firstErr error
	if err := d.flushInserts(); err != nil {
		firstErr = err
	}
	if d.markSQL != nil {
		if err := d.markSQL.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if d.getSQL != nil {
		if err := d.getSQL.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if d.db != nil {
		if err := d.db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if d.tmpDir != "" {
		if err := os.RemoveAll(d.tmpDir); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
