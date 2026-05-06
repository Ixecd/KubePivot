// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package metrics

import (
	"testing"
	"time"
)

func TestCarbonSDKClient_DefaultURL(t *testing.T) {
	c := NewCarbonSDKClient("")
	if c.BaseURL == "" {
		t.Error("empty baseURL should default to CarbonSDK")
	}
}

func TestCarbonSDKClient_CustomURL(t *testing.T) {
	c := NewCarbonSDKClient("https://custom-carbon.example.com")
	if c.BaseURL != "https://custom-carbon.example.com" {
		t.Errorf("BaseURL = %s, want custom", c.BaseURL)
	}
}

func TestCarbonCacheTTL(t *testing.T) {
	if carbonCacheTTL != 15*time.Minute {
		t.Errorf("carbonCacheTTL = %v, want 15min", carbonCacheTTL)
	}
}

func TestCarbonPoint_Fields(t *testing.T) {
	ts := time.Now()
	cp := CarbonPoint{Timestamp: ts, Intensity: 250.0}
	if cp.Intensity != 250.0 {
		t.Errorf("Intensity = %.1f, want 250.0", cp.Intensity)
	}
	if !cp.Timestamp.Equal(ts) {
		t.Error("Timestamp mismatch")
	}
}
