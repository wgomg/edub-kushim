package mirror

import (
	"testing"
)

func TestAvailable_DetectsRsync(t *testing.T) {
	got := Available()
	// Real CI environments with rsync installed should report true; CI without rsync (unlikely)
	// should report false. Either outcome is acceptable; we only assert consistency with LookPath.
	if got {
		t.Log("rsync is installed")
	} else {
		t.Log("rsync is not installed")
	}
}

func TestIsRemoteTarget(t *testing.T) {
	if !IsRemoteTarget("user@host:/path") {
		t.Error("expected user@host:/path to be remote")
	}
	if !IsRemoteTarget("rsync://host/module") {
		t.Error("expected rsync:// URL to be remote")
	}
	if IsRemoteTarget("/var/mirror") {
		t.Error("expected /var/mirror to be local")
	}
}

func TestParseStats_Stats2Format(t *testing.T) {
	input := "\n" +
		"sending incremental file list\n" +
		"Number of files: 12 (reg: 10, dir: 2)\n" +
		"Number of created files: 5 (reg: 5)\n" +
		"Number of deleted files: 0\n" +
		"Number of regular files transferred: 5\n" +
		"Total file size: 123,456,789 bytes\n" +
		"Literal data: 1,024 bytes\n"
	res := parseStats(input)
	if res.Files != 10 {
		t.Errorf("Files = %d, want 10 (from reg: count)", res.Files)
	}
	if res.Bytes != 123456789 {
		t.Errorf("Bytes = %d, want 123456789", res.Bytes)
	}
	if res.Transferred != 5 {
		t.Errorf("Transferred = %d, want 5", res.Transferred)
	}
	if res.Created != 5 {
		t.Errorf("Created = %d, want 5 (reg part)", res.Created)
	}
	if res.Deleted != 0 {
		t.Errorf("Deleted = %d, want 0", res.Deleted)
	}
	if res.Updated != 0 {
		t.Errorf("Updated = %d, want 0 (transferred - created)", res.Updated)
	}
}

func TestParseStats_DeletedFileLinesIgnored(t *testing.T) {
	// Per-file DEL lines from --info=del2 should not be parsed; only stats2 summary.
	input := "del\ndel\ndel\nNumber of files: 3 (reg: 2, dir: 1)\nTotal file size: 50 bytes\n"
	res := parseStats(input)
	if res.Files != 2 {
		t.Errorf("Files = %d, want 2", res.Files)
	}
	if res.Bytes != 50 {
		t.Errorf("Bytes = %d, want 50", res.Bytes)
	}
}

func TestParseStats_EmptyAndMalformed(t *testing.T) {
	if res := parseStats(""); res.Files != 0 || res.Bytes != 0 {
		t.Errorf("empty input: got %+v, want zero", res)
	}
	if res := parseStats("garbage output"); res.Files != 0 || res.Bytes != 0 {
		t.Errorf("garbage: got %+v, want zero (degrade rather than fail)", res)
	}
}

func TestParseStats_CommasInNumbers(t *testing.T) {
	// rsync formats both the outer total and the per-kind breakdown with
	// comma_num() thousands separators; parseCount strips them all.
	res := parseStats("Number of files: 1,234,567 (reg: 1000, dir: 234)\nTotal file size: 9,999,999,999 bytes\n")
	if res.Files != 1000 {
		t.Errorf("Files = %d, want 1000 (reg count, unformatted)", res.Files)
	}
	if res.Bytes != 9999999999 {
		t.Errorf("Bytes = %d, want 9999999999", res.Bytes)
	}
}

func TestParseStats_CommaFormattedBreakdown(t *testing.T) {
	// Large trees: the reg: breakdown itself is comma-formatted, e.g. "reg: 139,524".
	res := parseStats("Number of files: 142,000 (reg: 139,524, dir: 2,476)\n")
	if res.Files != 139524 {
		t.Errorf("Files = %d, want 139524 (full comma-grouped reg count)", res.Files)
	}
}

func TestParseStats_FullStats2Block(t *testing.T) {
	input := "\n" +
		"sending incremental file list\n" +
		"Number of files: 142,000 (reg: 139,524, dir: 2,476)\n" +
		"Number of created files: 1,048 (reg: 1,046, dir: 2)\n" +
		"Number of deleted files: 0\n" +
		"Number of regular files transferred: 1,398\n" +
		"Total file size: 80,080,709,948 bytes\n" +
		"Literal data: 1,024 bytes\n" +
		"Matched data: 0 bytes\n" +
		"File list size: 0\n" +
		"File list generation time: 0.001 seconds\n" +
		"File list transfer time: 0.000 seconds\n" +
		"Total bytes sent: 1,024\n" +
		"Total bytes received: 1,024\n"
	res := parseStats(input)
	if res.Files != 139524 {
		t.Errorf("Files = %d, want 139524", res.Files)
	}
	if res.Bytes != 80080709948 {
		t.Errorf("Bytes = %d, want 80080709948", res.Bytes)
	}
	if res.Transferred != 1398 {
		t.Errorf("Transferred = %d, want 1398", res.Transferred)
	}
	if res.Created != 1046 {
		t.Errorf("Created = %d, want 1046 (reg part)", res.Created)
	}
	if res.Deleted != 0 {
		t.Errorf("Deleted = %d, want 0", res.Deleted)
	}
	if res.Updated != 352 {
		t.Errorf("Updated = %d, want 352 (transferred - created)", res.Updated)
	}
}

func TestParseStats_NoOpRun(t *testing.T) {
	// No-op run: all counts 0 and the created/deleted parentheticals are omitted.
	input := "\n" +
		"sending incremental file list\n" +
		"Number of files: 142,000 (reg: 139,524, dir: 2,476)\n" +
		"Number of created files: 0\n" +
		"Number of deleted files: 0\n" +
		"Number of regular files transferred: 0\n" +
		"Total file size: 80,080,709,948 bytes\n"
	res := parseStats(input)
	if res.Files != 139524 {
		t.Errorf("Files = %d, want 139524", res.Files)
	}
	if res.Transferred != 0 || res.Created != 0 || res.Deleted != 0 || res.Updated != 0 {
		t.Errorf("counts = %+v, want all zero (Updated clamped at 0)", res)
	}
}

func TestParseStats_DeletedWithBreakdown(t *testing.T) {
	// Deletions > 0: the deleted line carries a "(reg: ..., dir: ...)" breakdown
	// after the total; Deleted must be the total including dirs.
	res := parseStats("Number of deleted files: 1,234 (reg: 1,200, dir: 34)\n")
	if res.Deleted != 1234 {
		t.Errorf("Deleted = %d, want 1234 (total incl. dirs)", res.Deleted)
	}
}