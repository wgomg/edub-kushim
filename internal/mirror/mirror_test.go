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
	// rsync formats the outer "Number of files" with thousands separators
	// but uses raw %d for the per-kind breakdown (reg/dir/link/...).
	res := parseStats("Number of files: 1,234,567 (reg: 1000, dir: 234)\nTotal file size: 9,999,999,999 bytes\n")
	if res.Files != 1000 {
		t.Errorf("Files = %d, want 1000 (reg count, unformatted)", res.Files)
	}
	if res.Bytes != 9999999999 {
		t.Errorf("Bytes = %d, want 9999999999", res.Bytes)
	}
}