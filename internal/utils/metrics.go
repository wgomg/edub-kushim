package utils

import (
	"bufio"
	"fmt"
	"os"
	"runtime/metrics"
	"strconv"
	"strings"
)

type MemSnapshot struct {
	HeapInUse uint64 // /memory/classes/heap/objects:bytes — live heap
	HeapAlloc uint64 // /gc/heap/allocs:bytes — cumulative allocated
	NumGC     uint64 // /gc/cycles/automatic:gc-cycles
	RSS       uint64 // VmRSS from /proc/self/status — total physical RAM (Go + C)
}

func ReadMemSnapshot() MemSnapshot {
	const (
		heapInUse = "/memory/classes/heap/objects:bytes"
		heapAlloc = "/gc/heap/allocs:bytes"
		numGC     = "/gc/cycles/automatic:gc-cycles"
	)

	samples := []metrics.Sample{
		{Name: heapInUse},
		{Name: heapAlloc},
		{Name: numGC},
	}
	metrics.Read(samples)

	return MemSnapshot{
		HeapInUse: samples[0].Value.Uint64(),
		HeapAlloc: samples[1].Value.Uint64(),
		NumGC:     samples[2].Value.Uint64(),
		RSS:       readVmRSS(),
	}
}

func readVmRSS() uint64 {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}

func FormatMemDelta(before, after MemSnapshot) string {
	heapDelta := int64(after.HeapInUse) - int64(before.HeapInUse)
	rssDelta := int64(after.RSS) - int64(before.RSS)
	gcs := after.NumGC - before.NumGC

	parts := ""
	if heapDelta >= 0 {
		parts = fmt.Sprintf("heap +%s", formatBytes(uint64(heapDelta)))
	} else {
		parts = fmt.Sprintf("heap -%s", formatBytes(uint64(-heapDelta)))
	}

	if rssDelta >= 0 {
		parts += fmt.Sprintf(", RSS +%s", formatBytes(uint64(rssDelta)))
	} else {
		parts += fmt.Sprintf(", RSS -%s", formatBytes(uint64(-rssDelta)))
	}

	if gcs > 0 {
		parts += fmt.Sprintf(", %d GC(s)", gcs)
	}
	return parts
}

func formatBytes(b uint64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
