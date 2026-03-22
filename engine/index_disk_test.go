package engine

import (
	"os"
	"testing"
	"time"
)

func TestDiskIndex_AddGetMarkUsedIterateUnused(t *testing.T) {
	idx, err := NewDiskIndex(t.TempDir())
	if err != nil {
		t.Fatalf("NewDiskIndex: %v", err)
	}
	defer idx.Close()

	tx1 := Transaction{ID: "r1", Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Amount: 100, Currency: "USD", Reference: "REF-1", Name: "A", Source: "right"}
	tx2 := Transaction{ID: "r2", Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Amount: 150, Currency: "USD", Reference: "REF-1", Name: "B", Source: "right"}
	tx3 := Transaction{ID: "r3", Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), Amount: 200, Currency: "USD", Reference: "REF-2", Name: "C", Source: "right"}

	if err := idx.Add(tx1); err != nil {
		t.Fatalf("Add tx1: %v", err)
	}
	if err := idx.Add(tx2); err != nil {
		t.Fatalf("Add tx2: %v", err)
	}
	if err := idx.Add(tx3); err != nil {
		t.Fatalf("Add tx3: %v", err)
	}

	cand, err := idx.Get("REF-1")
	if err != nil {
		t.Fatalf("Get REF-1: %v", err)
	}
	if got, want := len(cand), 2; got != want {
		t.Fatalf("Get REF-1 len=%d, want %d", got, want)
	}
	if err := idx.MarkUsed(cand[0]); err != nil {
		t.Fatalf("MarkUsed: %v", err)
	}

	cand2, err := idx.Get("REF-1")
	if err != nil {
		t.Fatalf("Get REF-1 after mark-used: %v", err)
	}
	if got, want := len(cand2), 1; got != want {
		t.Fatalf("Get REF-1 after mark-used len=%d, want %d", got, want)
	}

	unused := map[string]bool{}
	if err := idx.IterateUnused(func(tx Transaction) error {
		unused[tx.ID] = true
		return nil
	}); err != nil {
		t.Fatalf("IterateUnused: %v", err)
	}

	if len(unused) != 2 || !unused["r2"] || !unused["r3"] {
		t.Fatalf("unexpected unused set: %#v", unused)
	}
}

func TestDiskIndex_CloseRemovesTempDir(t *testing.T) {
	idx, err := NewDiskIndex(t.TempDir())
	if err != nil {
		t.Fatalf("NewDiskIndex: %v", err)
	}
	d, ok := idx.(*diskIndex)
	if !ok {
		t.Fatalf("expected *diskIndex, got %T", idx)
	}
	tmpDir := d.tmpDir

	if err := idx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Fatalf("expected temp dir removed, stat err=%v", err)
	}
}
