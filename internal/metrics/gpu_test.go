// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package metrics

import (
	"strings"
	"testing"
	"time"
)

// ── GPUStalenessError ───────────────────────────────────────────

func TestGPUStalenessError(t *testing.T) {
	ts := time.Now().Add(-90 * time.Second)
	err := &GPUStalenessError{NodeName: "gpu-node-1", LastSample: ts}
	msg := err.Error()
	if msg == "" {
		t.Error("empty error message")
	}
	// Check that the message mentions staleness
	if !strings.Contains(msg, "stale") && !strings.Contains(msg, "GPU") {
		t.Errorf("unexpected error message: %s", msg)
	}
}

// ── DefaultGPUStalenessThreshold ────────────────────────────────

func TestDefaultGPUStalenessThreshold(t *testing.T) {
	if DefaultGPUStalenessThreshold != 60*time.Second {
		t.Errorf("DefaultGPUStalenessThreshold = %v, want 60s", DefaultGPUStalenessThreshold)
	}
}

