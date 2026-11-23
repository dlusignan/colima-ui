package dockerc

// CPUPercent applies Docker's formula to two readings of one container:
// cpuDelta / systemDelta * onlineCPUs * 100. It returns 0 when either delta is
// not positive (first reading, restarted container, identical samples), so the
// unsigned counters never underflow.
func CPUPercent(prev, cur Reading) float64 {
	if cur.CPUTotalNanos <= prev.CPUTotalNanos || cur.SystemNanos <= prev.SystemNanos {
		return 0
	}
	cpuDelta := float64(cur.CPUTotalNanos - prev.CPUTotalNanos)
	sysDelta := float64(cur.SystemNanos - prev.SystemNanos)
	cpus := cur.OnlineCPUs
	if cpus == 0 {
		cpus = 1
	}
	return cpuDelta / sysDelta * float64(cpus) * 100
}

// memoryBytes follows the Docker CLI: usage minus inactive file cache
// (inactive_file on cgroup v2, total_inactive_file on v1), clamped at 0.
func memoryBytes(usage uint64, stats map[string]uint64) uint64 {
	cache, ok := stats["inactive_file"] // cgroup v2
	if !ok {
		cache = stats["total_inactive_file"] // cgroup v1, 0 if absent
	}
	if cache >= usage {
		return 0
	}
	return usage - cache
}
