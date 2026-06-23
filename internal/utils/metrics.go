package utils

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"time"
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

// MemFull adds runtime.MemStats fields (HeapSys, HeapIdle, HeapReleased) that
// are not available through runtime/metrics. Use this for diagnostic logging
// when you need to know how much memory Go is holding vs. has returned to the OS.
type MemFull struct {
	MemSnapshot
	HeapSys      uint64 // bytes obtained from OS for the heap
	HeapIdle     uint64 // bytes in idle spans
	HeapReleased uint64 // bytes returned to the OS
}

func ReadMemFull() MemFull {
	snap := ReadMemSnapshot()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	return MemFull{
		MemSnapshot:  snap,
		HeapSys:      ms.HeapSys,
		HeapIdle:     ms.HeapIdle,
		HeapReleased: ms.HeapReleased,
	}
}

// FormatMemFull returns a one-line summary of key memory metrics. The format:
//
//	RSS: 2.3 GiB | heap in-use: 1.1 GiB | heap sys: 3.0 GiB | heap idle: 1.9 GiB | heap released: 0.5 GiB | GCs: 14
func FormatMemFull(m MemFull) string {
	return fmt.Sprintf(
		"RSS: %s | heap in-use: %s | heap sys: %s | heap idle: %s | heap released: %s | GCs: %d",
		FormatBytes(m.RSS),
		FormatBytes(m.HeapInUse),
		FormatBytes(m.HeapSys),
		FormatBytes(m.HeapIdle),
		FormatBytes(m.HeapReleased),
		m.NumGC,
	)
}

func FormatMemDelta(before, after MemSnapshot) string {
	heapDelta := int64(after.HeapInUse) - int64(before.HeapInUse)
	rssDelta := int64(after.RSS) - int64(before.RSS)
	gcs := after.NumGC - before.NumGC

	parts := ""
	if heapDelta >= 0 {
		parts = fmt.Sprintf("heap +%s", FormatBytes(uint64(heapDelta)))
	} else {
		parts = fmt.Sprintf("heap -%s", FormatBytes(uint64(-heapDelta)))
	}

	if rssDelta >= 0 {
		parts += fmt.Sprintf(", RSS +%s", FormatBytes(uint64(rssDelta)))
	} else {
		parts += fmt.Sprintf(", RSS -%s", FormatBytes(uint64(-rssDelta)))
	}

	if gcs > 0 {
		parts += fmt.Sprintf(", %d GC(s)", gcs)
	}
	return parts
}

func FormatBytes(b uint64) string {
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

func HumanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		d = d.Round(time.Second)
		m := int(d.Minutes())
		s := int(d.Seconds()) - m*60
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		d = d.Round(time.Second)
		h := int(d.Hours())
		d -= time.Duration(h) * time.Hour
		m := int(d.Minutes())
		s := int(d.Seconds()) - m*60
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
}
