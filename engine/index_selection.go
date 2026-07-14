package engine

import "fmt"

// IndexSelection describes the resource decision made before a streaming run.
// It is intentionally additive so existing result consumers can ignore it.
type IndexSelection struct {
	RequestedBackend       string          `json:"requested_backend"`
	Backend                string          `json:"backend"`
	Reason                 string          `json:"reason"`
	EstimatedMemoryBytes   int64           `json:"estimated_memory_bytes,omitempty"`
	EstimatedTempDiskBytes int64           `json:"estimated_temp_disk_bytes,omitempty"`
	Fallbacks              []IndexFallback `json:"fallbacks,omitempty"`
}

// IndexFallback records why a candidate backend was not selected.
type IndexFallback struct {
	Backend string `json:"backend"`
	Reason  string `json:"reason"`
}

func (s IndexSelection) String() string {
	return fmt.Sprintf("backend=%s reason=%s", s.Backend, s.Reason)
}
