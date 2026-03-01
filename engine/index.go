package engine

import "time"

// bucket holds a right-side transaction in a compact, pointer-lean form.
//
// Date is stored as Unix nanoseconds (int64) rather than time.Time, eliminating
// the time.Time.loc (*Location) pointer from every bucket. At 20M entries that
// removes 20M pointer fields from the GC scan graph per collection cycle.
//
// Reference is NOT stored here — it is available as the map key in
// memoryIndex.data and is injected by toTransaction(ref) at retrieval time.
// This eliminates a second duplicate string pointer per bucket.
//
// Pointer fields retained: id, currency, name, source (4 strings).
// Pointer fields eliminated vs. previous design: tx.Date.loc, tx.Reference (2 pointers).
type bucket struct {
	id       string
	dateUnix int64 // tx.Date.UnixNano() — no *Location pointer
	amount   int64
	currency string
	name     string
	source   string
	used     bool
}

// toTransaction reconstructs a full Transaction from a bucket for output events.
// ref must be the map key under which this bucket is stored.
func (b *bucket) toTransaction(ref string) Transaction {
	return Transaction{
		ID:        b.id,
		Date:      time.Unix(0, b.dateUnix).UTC(),
		Amount:    b.amount,
		Currency:  b.currency,
		Reference: ref,
		Name:      b.name,
		Source:    b.source,
	}
}

// RightIndex is the right-side lookup store for ReconcileStreaming.
//
// Not thread-safe. Single-threaded use only.
// Close() must be called when the index is no longer needed.
//
// The default in-memory implementation (NewMemoryIndex) requires the right side
// to fit in RAM. For datasets where the right side exceeds available memory, a
// disk-backed implementation of this interface can be substituted.
// ReconcileStreaming accepts any RightIndex without modification — it is the
// only coupling point between the reconciler and the storage backend.
type RightIndex interface {
	// Add inserts a transaction into the index.
	// Implementations strip Raw and convert Date to a pointer-free representation
	// before storage. Callers should not assume Raw or Date are preserved as-is.
	Add(tx Transaction) error

	// Get returns all buckets matching the given reference key.
	// Callers may set bucket.used = true to mark a bucket as consumed.
	Get(ref string) []*bucket

	// IterateUnused calls fn for every transaction not marked as used.
	// Iteration order is unspecified.
	IterateUnused(fn func(tx Transaction) error) error

	// Close releases any resources held by the index (file handles, temp files, etc.).
	Close() error
}

// memoryIndex is the default in-memory RightIndex implementation.
//
// Memory: each bucket stores 4 strings + 2 int64s + 1 bool. Reference and Raw
// are not stored per-bucket (Reference is the map key; Raw is stripped at Add).
// Date is stored as int64 Unix nanos — no *Location pointer.
// At 20M right-side rows, expect 4–6 GB depending on average string lengths.
//
// Documented limit: if the right side does not fit in RAM, implement RightIndex
// backed by SQLite, mmap, or another disk store and pass it to ReconcileStreaming.
type memoryIndex struct {
	data map[string][]*bucket
}

// NewMemoryIndex returns a new empty in-memory RightIndex.
func NewMemoryIndex() RightIndex {
	return &memoryIndex{
		data: make(map[string][]*bucket),
	}
}

func (m *memoryIndex) Add(tx Transaction) error {
	// Store compact bucket — no Raw, no time.Time, no duplicate Reference.
	b := &bucket{
		id:       tx.ID,
		dateUnix: tx.Date.UnixNano(),
		amount:   tx.Amount,
		currency: tx.Currency,
		name:     tx.Name,
		source:   tx.Source,
	}
	m.data[tx.Reference] = append(m.data[tx.Reference], b)
	return nil
}

func (m *memoryIndex) Get(ref string) []*bucket {
	return m.data[ref]
}

func (m *memoryIndex) IterateUnused(fn func(tx Transaction) error) error {
	for ref, buckets := range m.data {
		for _, b := range buckets {
			if !b.used {
				if err := fn(b.toTransaction(ref)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (m *memoryIndex) Close() error {
	m.data = nil
	return nil
}
