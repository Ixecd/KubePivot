// internal/scheduler/parser_test.go
package scheduler

import "testing"

func TestParseCPU_validUnits(t *testing.T) {
    tests := []struct{
        raw  string
        want int64
    }{
        {"100m", 100},
        {"1", 1000},
        {"2.5", 2500},
        {"0.5", 500},
        {"", 0},
    }
    for _, tt := range tests {
        got, _ := parseCPU(tt.raw)
        if got != tt.want {
            t.Errorf("parseCPU(%q) = %d, want %d", tt.raw, got, tt.want)
        }
    }
}

func TestParseMemory_validUnits(t *testing.T) {
    tests := []struct{
        raw  string
        want int64
    }{
        {"128Mi", 128 * 1024 * 1024},
        {"512Ki", 512 * 1024},
        {"1Gi", 1 * 1024 * 1024 * 1024},
        {"512M", 512 * 1000 * 1000},
        {"", 0},
    }
    for _, tt := range tests {
        got, _ := parseMemory(tt.raw)
        if got != tt.want {
            t.Errorf("parseMemory(%q) = %d, want %d", tt.raw, got, tt.want)
        }
    }
}