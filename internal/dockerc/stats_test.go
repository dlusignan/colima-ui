package dockerc

import (
	"math"
	"testing"
)

func TestCPUPercent(t *testing.T) {
	tests := []struct {
		name      string
		prev, cur Reading
		want      float64
	}{
		{"first reading", Reading{}, Reading{CPUTotalNanos: 500, SystemNanos: 1000, OnlineCPUs: 4}, 500.0 / 1000 * 4 * 100},
		{"two cpus half busy", Reading{CPUTotalNanos: 100, SystemNanos: 1000}, Reading{CPUTotalNanos: 200, SystemNanos: 1200, OnlineCPUs: 2}, 100.0 / 200 * 2 * 100},
		{"zero cpu delta", Reading{CPUTotalNanos: 100, SystemNanos: 1000}, Reading{CPUTotalNanos: 100, SystemNanos: 2000, OnlineCPUs: 2}, 0},
		{"zero system delta", Reading{CPUTotalNanos: 100, SystemNanos: 1000}, Reading{CPUTotalNanos: 300, SystemNanos: 1000, OnlineCPUs: 2}, 0},
		{"counter reset", Reading{CPUTotalNanos: 900, SystemNanos: 9000}, Reading{CPUTotalNanos: 10, SystemNanos: 9500, OnlineCPUs: 2}, 0},
		{"system reset", Reading{CPUTotalNanos: 100, SystemNanos: 9000}, Reading{CPUTotalNanos: 200, SystemNanos: 10, OnlineCPUs: 2}, 0},
		{"unknown cpus counts as one", Reading{CPUTotalNanos: 0, SystemNanos: 0}, Reading{CPUTotalNanos: 50, SystemNanos: 100}, 50},
		{"identical readings", Reading{CPUTotalNanos: 5, SystemNanos: 5, OnlineCPUs: 1}, Reading{CPUTotalNanos: 5, SystemNanos: 5, OnlineCPUs: 1}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CPUPercent(tc.prev, tc.cur); math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestMemoryBytes(t *testing.T) {
	tests := []struct {
		name  string
		usage uint64
		stats map[string]uint64
		want  uint64
	}{
		{"cgroup v2", 1000, map[string]uint64{"inactive_file": 300, "file": 500}, 700},
		{"cgroup v1", 1000, map[string]uint64{"total_inactive_file": 400, "cache": 600}, 600},
		{"v2 wins over v1 key", 1000, map[string]uint64{"inactive_file": 100, "total_inactive_file": 900}, 900},
		{"no key plain usage", 1000, map[string]uint64{"anon": 1}, 1000},
		{"nil stats", 1000, nil, 1000},
		{"clamp v2", 100, map[string]uint64{"inactive_file": 300}, 0},
		{"clamp v1", 100, map[string]uint64{"total_inactive_file": 100}, 0},
		{"zero usage", 0, map[string]uint64{"inactive_file": 5}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := memoryBytes(tc.usage, tc.stats); got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
}
