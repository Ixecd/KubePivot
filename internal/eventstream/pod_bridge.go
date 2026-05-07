// internal/eventstream/pod_bridge.go — v3.2: Pod Informer → PodCache 桥接
//
// PodCacheBridge 订阅 Informer Pod Watch 事件，自动将 Resource 转为 PodEntry
// 并写入 PodCache。实现 Watch 接线：Informer Watch → Resource → PodEntry → PodCache。

package eventstream

import (
	"encoding/json"
	"log/slog"
	"strconv"
)

// PodCacheBridge subscribes to an Informer for Pod resources and populates a PodCache.
// This is the Watch wiring: Informer Watch → Resource → PodEntry → PodCache.Put/Delete.
type PodCacheBridge struct {
	cache    *PodCache
	informer Informer
	sub      Subscription
}

// NewPodCacheBridge creates a bridge and subscribes to the Informer.
// informer must be a Pod informer (Kind="Pod"). Non-Pod events are silently dropped.
func NewPodCacheBridge(informer Informer, cache *PodCache) *PodCacheBridge {
	b := &PodCacheBridge{cache: cache, informer: informer}
	b.sub = informer.Subscribe(func(event Event) {
		b.handleEvent(event)
	})
	slog.Info("PodCacheBridge: subscribed to Pod informer")
	return b
}

// Unsubscribe stops the bridge.
func (b *PodCacheBridge) Unsubscribe() {
	if b.sub != nil {
		b.sub.Unsubscribe()
	}
}

func (b *PodCacheBridge) handleEvent(event Event) {
	switch event.Type {
	case EventAdd, EventUpdate:
		if event.New == nil || event.New.Kind != "Pod" {
			return
		}
		entry, err := ResourceToPodEntry(event.New)
		if err != nil {
			slog.Warn("PodCacheBridge: failed to convert Resource to PodEntry",
				"pod", event.New.Key(), "err", err)
			return
		}
		b.cache.Put(entry, "")

	case EventDelete:
		if event.Old == nil {
			return
		}
		b.cache.Delete(event.Old.Namespace, event.Old.Name, "")

	case EventResync:
		// Full resync: list all from informer cache and PutBulk
		resources := b.informer.ListAll()
		entries := make([]*PodEntry, 0, len(resources))
		for _, r := range resources {
			if r.Kind != "Pod" {
				continue
			}
			entry, err := ResourceToPodEntry(r)
			if err != nil {
				continue
			}
			entries = append(entries, entry)
		}
		b.cache.PutBulk(entries)
	}
}

// ─── Resource → PodEntry 转换 ─────────────────────────────────

// podJSON is a minimal subset of v1.Pod for PodEntry conversion.
type podJSON struct {
	Spec struct {
		NodeName   string `json:"nodeName"`
		Containers []struct {
			Resources struct {
				Requests struct {
					CPU    string `json:"cpu"`
					Memory string `json:"memory"`
				} `json:"requests"`
			} `json:"resources"`
		} `json:"containers"`
	} `json:"spec"`
}

// ResourceToPodEntry converts an Informer Resource (Kind=Pod) to a PodEntry.
// Extracts NodeName + container resource requests from RawJSON (full Pod object).
// RV is parsed from Resource.ResourceVersion (string → int64).
func ResourceToPodEntry(r *Resource) (*PodEntry, error) {
	var pod podJSON
	if err := json.Unmarshal(r.RawJSON, &pod); err != nil {
		return nil, err
	}
	spec := pod.Spec

	var cpu, mem int64
	for _, c := range spec.Containers {
		cpu += parseQuantityToMilli(c.Resources.Requests.CPU)
		mem += parseQuantityToBytes(c.Resources.Requests.Memory)
	}

	rv, _ := strconv.ParseInt(r.ResourceVersion, 10, 64)

	e := &PodEntry{
		Namespace: r.Namespace,
		Name:      r.Name,
		NodeName:  spec.NodeName,
		Phase:     r.Phase,
		Requests:  ResourceRequest{CPU: cpu, Memory: mem},
		RV:        rv,
	}
	e.SetLabels(r.Labels)
	return e, nil
}

// parseQuantityToMilli parses a K8s quantity string to milli-units.
// Handles formats: "100m", "1", "1.5", "500m".
func parseQuantityToMilli(s string) int64 {
	if s == "" {
		return 0
	}
	if len(s) > 1 && s[len(s)-1] == 'm' {
		v, _ := strconv.ParseInt(s[:len(s)-1], 10, 64)
		return v
	}
	// Assume integer cores → convert to millicores
	v, _ := strconv.ParseFloat(s, 64)
	return int64(v * 1000)
}

// parseQuantityToBytes parses a K8s memory quantity string to bytes.
// Handles: "128Mi", "1Gi", "512M", "1024", "1G".
func parseQuantityToBytes(s string) int64 {
	if s == "" {
		return 0
	}
	// Strip suffix
	var multiplier int64 = 1
	switch {
	case len(s) > 2 && s[len(s)-2:] == "Gi":
		multiplier = 1024 * 1024 * 1024
		s = s[:len(s)-2]
	case len(s) > 2 && s[len(s)-2:] == "Mi":
		multiplier = 1024 * 1024
		s = s[:len(s)-2]
	case len(s) > 2 && s[len(s)-2:] == "Ki":
		multiplier = 1024
		s = s[:len(s)-2]
	case len(s) > 1 && s[len(s)-1] == 'G':
		multiplier = 1000 * 1000 * 1000
		s = s[:len(s)-1]
	case len(s) > 1 && s[len(s)-1] == 'M':
		multiplier = 1000 * 1000
		s = s[:len(s)-1]
	case len(s) > 1 && s[len(s)-1] == 'K':
		multiplier = 1000
		s = s[:len(s)-1]
	}
	v, _ := strconv.ParseFloat(s, 64)
	return int64(v * float64(multiplier))
}
