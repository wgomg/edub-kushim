package utils

import (
	"fmt"
	"runtime/metrics"
)

type MemSnapshot struct {
	HeapInUse uint64 // /memory/classes/heap/objects:bytes — live heap
	HeapAlloc uint64 // /gc/heap/allocs:bytes — cumulative allocated
	NumGC     uint64 // /gc/cycles/automatic:gc-cycles
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
	}
}

func FormatMemDelta(before, after MemSnapshot) string {
	delta := int64(after.HeapInUse) - int64(before.HeapInUse)
	gcs := after.NumGC - before.NumGC

	parts := ""
	if delta >= 0 {
		parts = fmt.Sprintf("heap +%s", formatBytes(uint64(delta)))
	} else {
		parts = fmt.Sprintf("heap -%s", formatBytes(uint64(-delta)))
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
