package eventstream

import (
	"testing"
	"time"
)

func TestAccessTracker_Record(t *testing.T) {
	at := NewAccessTracker()
	if at.Record("key1") {
		t.Error("first access should not promote")
	}
	if at.Record("key1") {
		t.Error("second access should not promote")
	}
	if !at.Record("key1") {
		t.Error("third access should promote to Hot")
	}
}

func TestAccessTracker_DemoteCold(t *testing.T) {
	at := NewAccessTracker()
	at.Record("key1")
	at.Record("key2")

	// All accessed recently — nothing cold
	cold := at.DemoteCold(time.Now())
	if len(cold) != 0 {
		t.Errorf("recently accessed keys should not be cold, got %v", cold)
	}

	// 31 minutes later — should be cold
	cold = at.DemoteCold(time.Now().Add(31 * time.Minute))
	if len(cold) != 2 {
		t.Errorf("old keys should be cold, got %d", len(cold))
	}
}

func TestPodEntryCompact_RoundTrip(t *testing.T) {
	e := &PodEntry{Namespace: "ns", Name: "p", NodeName: "n1", Phase: "Running",
		Requests: ResourceRequest{CPU: 500, Memory: 512 << 20}, RV: 42}
	c := e.Compact()
	if c.Namespace != "ns" || c.Name != "p" {
		t.Error("compact loses identity")
	}
	if c.NodeName != "n1" || c.Phase != "Running" {
		t.Error("compact loses scheduling fields")
	}
	if c.RV != 42 {
		t.Error("compact loses RV")
	}
	e2 := c.Expand()
	if e2.Namespace != "ns" || e2.Name != "p" {
		t.Error("expand loses identity")
	}
}
