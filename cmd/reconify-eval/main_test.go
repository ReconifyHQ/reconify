package main

import "testing"

func TestAbsoluteReconifyPathRequiresValue(t *testing.T) {
	if _, err := absoluteReconifyPath(""); err == nil || err.Error() != "--reconify is required" {
		t.Fatalf("empty path error = %v", err)
	}
}
