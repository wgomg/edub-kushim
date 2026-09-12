package tagreconcile

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

func TestParseBootstrapLogs(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "hugot.log"),
		[]byte("2026/08/01 02:00:00 INFO  : REQID=doc-1 hugot: consolidate \"capitalism\" → anti capitalism (0.912)\n"+
			"2026/08/01 02:00:01 INFO  : REQID=doc-2 hugot: consolidate \"capitalism\" → anti capitalism (0.915)\n"+
			"2026/08/01 02:00:02 INFO  : REQID=doc-3 hugot: no matches above min_sim=0.40\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kushim-2026-08-02T03-00-00.000.log"),
		[]byte("2026/08/02 03:00:00 INFO  : REQID=doc-4 consolidate \"socialism\" → communism (0.880)\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kushim.log"),
		[]byte("2026/08/03 04:00:00 INFO  : REQID=doc-5 consolidation[v1]: keep \"anarchism\" (sim=0.870 reason=ambiguous_band)\n"+
			"2026/08/03 04:00:01 INFO  : REQID=doc-6 consolidation[v2]: keep \"fascism\" (sim=0.860 reason=ambiguous_band verdict=variant cached=false)\n"+
			"2026/08/03 04:00:02 INFO  : REQID=doc-7 consolidation[v1]: keep \"liberalism\" (sim=0.850 reason=negation_guard)\n"+
			"2026/08/03 04:00:03 INFO  : REQID=doc-8 consolidation[v1]: replace \"marxism\" → \"communism\" (sim=0.950 runner=0.900 reason=auto_replace)\n"+
			"2026/08/03 04:00:04 INFO  : REQID=doc-9 consolidation[v1]: keep \"capitalism\" (sim=0.912 reason=ambiguous_band verdict=same cached=true)\n"), 0644); err != nil {
		t.Fatal(err)
	}

	pairs := parseBootstrapLogs(dir, testutil.NewTestLogger())

	cap := pairs["capitalism\x00anti capitalism"]
	if cap == nil {
		t.Fatalf("missing absorbed pair, got keys: %v", keys(pairs))
	}
	if !cap.absorbed || cap.sim != 0.915 || len(cap.docIDs) != 2 || cap.docIDs[0] != "doc-1" || cap.docIDs[1] != "doc-2" {
		t.Fatalf("absorbed pair = %+v", cap)
	}
	if cap.lastTime.IsZero() {
		t.Fatalf("absorbed pair lastTime = zero")
	}

	soc := pairs["socialism\x00communism"]
	if soc == nil || !soc.absorbed || soc.sim != 0.880 || len(soc.docIDs) != 1 {
		t.Fatalf("old kushim pair = %+v", soc)
	}

	an := pairs["anarchism"]
	if an == nil || an.absorbed || an.target != "" || an.sim != 0.870 || len(an.docIDs) != 1 {
		t.Fatalf("band-keep pair = %+v", an)
	}

	fa := pairs["fascism"]
	if fa == nil || fa.verdict != types.TagVerdicts.Variant || fa.sim != 0.860 {
		t.Fatalf("band-keep verdict pair = %+v", fa)
	}

	if pairs["liberalism"] != nil || pairs["marxism\x00communism"] != nil {
		t.Fatalf("guard/auto_replace lines must not be parsed: %v", keys(pairs))
	}

	capKeep := pairs["capitalism"]
	if capKeep == nil || capKeep.verdict != types.TagVerdicts.Same || capKeep.absorbed {
		t.Fatalf("verdict=same keep = %+v", capKeep)
	}

	if pairs["doc-3"] != nil {
		t.Fatal("unknown line must be ignored")
	}
}

func TestParseBootstrapLogsGz(t *testing.T) {
	dir := t.TempDir()
	gzPath := filepath.Join(dir, "kushim-2026-08-01T00-00-00.000.log.gz")
	if err := writeGzFile(t, gzPath, "2026/08/01 02:00:00 INFO  : REQID=doc-1 consolidate \"alpha\" → beta (0.900)\n"); err != nil {
		t.Fatal(err)
	}
	pairs := parseBootstrapLogs(dir, testutil.NewTestLogger())
	p := pairs["alpha\x00beta"]
	if p == nil || !p.absorbed || len(p.docIDs) != 1 || p.docIDs[0] != "doc-1" {
		t.Fatalf("gz pair = %+v", p)
	}
}

func TestParseBootstrapLogsMissingDir(t *testing.T) {
	pairs := parseBootstrapLogs(t.TempDir(), testutil.NewTestLogger())
	if len(pairs) != 0 {
		t.Fatalf("empty dir: pairs = %v", pairs)
	}
}

func TestParseBootstrapLogsUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "hugot.log")
	if err := os.WriteFile(bad, []byte("2026/08/01 02:00:00 INFO  : REQID=doc-1 hugot: consolidate \"x\" → y (0.900)\n"), 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(bad, 0644) })
	pairs := parseBootstrapLogs(dir, testutil.NewTestLogger())
	if len(pairs) != 0 {
		t.Fatalf("unreadable: pairs = %v", pairs)
	}
}

func TestParseLineUnanchoredREQID(t *testing.T) {
	// REQID is mid-line, not at column 0 — the timestamp prefix precedes it.
	pairs := map[string]*parsedPair{}
	parseLine("2026/09/01 12:00:00 INFO  : REQID=doc-99 hugot: consolidate \"x\" → y (0.900)", pairs)
	if p := pairs["x\x00y"]; p == nil || !p.absorbed || p.docIDs[0] != "doc-99" {
		t.Fatalf("unanchored REQID parse: %+v", p)
	}
}

func TestLineTime(t *testing.T) {
	got := lineTime("2026/09/10 12:34:56 INFO  : foo")
	want := time.Date(2026, 9, 10, 12, 34, 56, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("lineTime = %v, want %v", got, want)
	}
	if !lineTime("garbage").IsZero() {
		t.Fatal("garbage must yield zero time")
	}
	if !lineTime("short").IsZero() {
		t.Fatal("short must yield zero time")
	}
}

func writeGzFile(t *testing.T, path, content string) error {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write([]byte(content)); err != nil {
		return err
	}
	if err := gw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}

func keys(m map[string]*parsedPair) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}