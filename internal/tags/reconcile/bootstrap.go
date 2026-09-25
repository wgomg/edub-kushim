package reconcile

import (
	"bufio"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/types"
)

// parsedPair accumulates the evidence of one ordered (query, target) pair
// across the parsed log files. Band-keep lines carry no target; their pairs
// are keyed by query alone (target == "")
type parsedPair struct {
	query    string
	target   string
	sim      float64
	absorbed bool
	verdict  types.TagVerdict
	docIDs   []string
	lastTime time.Time
}

var (
	// hugot.log (pre-Stage-0): hugot: consolidate "<query>" → <target> (<sim>)
	reHugotConsolidate = regexp.MustCompile(`REQID=(\S+) hugot: consolidate "([^"]+)" → (.+) \((\d+\.\d+)\)$`)
	// kushim.log (Stage 0→1 window): consolidate "<query>" → <target> (<sim>)
	reKushimConsolidate = regexp.MustCompile(`REQID=(\S+) consolidate "([^"]+)" → (.+) \((\d+\.\d+)\)$`)
	// kushim.log (Stage 1+): consolidation[vN]: keep "<query>" (sim=<s> reason=ambiguous_band [verdict=<v> cached=<b>])
	reBandKeep = regexp.MustCompile(`REQID=(\S+) consolidation\[v\d+\]: keep "([^"]+)" \(sim=(\d+\.\d+) reason=ambiguous_band(?: verdict=(\w+)(?: cached=\w+)?)?\)$`)
	// kushim.log (Stage 1+): consolidation[vN]: keep "<query>" (reason=rank_unavailable)
	reRankUnavailableKeep = regexp.MustCompile(`REQID=(\S+) consolidation\[v\d+\]: keep "([^"]+)" \(reason=rank_unavailable\)$`)
)

func parseBootstrapLogs(logsDir string, logger *log.Logger) map[string]*parsedPair {
	pairs := make(map[string]*parsedPair)
	for _, prefix := range []string{"hugot", "kushim"} {
		// active file plus lumberjack rotations (timestamped siblings, .gz)
		files, err := filepath.Glob(filepath.Join(logsDir, prefix+"*"))
		if err != nil {
			logger.Debug("", "tag reconcile bootstrap: %s glob failed: %v", prefix, err)
			continue
		}
		if len(files) == 0 {
			logger.Debug("", "tag reconcile bootstrap: %s.log not found — skipping", prefix)
			continue
		}
		sort.Strings(files)
		for _, f := range files {
			if err := parseLogFile(f, pairs); err != nil {
				logger.Debug("", "tag reconcile bootstrap: %s unreadable: %v", f, err)
			}
		}
	}
	return pairs
}

func parseLogFile(path string, pairs map[string]*parsedPair) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		parseLine(sc.Text(), pairs)
	}
	return sc.Err()
}

func parseLine(line string, pairs map[string]*parsedPair) {
	if m := reHugotConsolidate.FindStringSubmatch(line); m != nil {
		addParsedPair(pairs, m[2], m[3], m[4], true, "", m[1], lineTime(line))
		return
	}
	if m := reKushimConsolidate.FindStringSubmatch(line); m != nil {
		addParsedPair(pairs, m[2], m[3], m[4], true, "", m[1], lineTime(line))
		return
	}
	if m := reBandKeep.FindStringSubmatch(line); m != nil {
		addParsedPair(pairs, m[2], "", m[3], false, types.TagVerdict(m[4]), m[1], lineTime(line))
		return
	}
	if m := reRankUnavailableKeep.FindStringSubmatch(line); m != nil {
		addParsedPair(pairs, m[2], "", "0", false, "", m[1], lineTime(line))
		return
	}
}

func addParsedPair(pairs map[string]*parsedPair, query, target, sim string, absorbed bool, verdict types.TagVerdict, docID string, t time.Time) {
	s, err := strconv.ParseFloat(sim, 64)
	if err != nil {
		return
	}
	key := query
	if target != "" {
		key = query + "\x00" + target
	}
	p := pairs[key]
	if p == nil {
		p = &parsedPair{query: query, target: target}
		pairs[key] = p
	}
	p.sim = s
	p.absorbed = p.absorbed || absorbed
	if verdict.Valid() {
		p.verdict = verdict
	}
	if docID != "" && !slicesContains(p.docIDs, docID) {
		p.docIDs = append(p.docIDs, docID)
	}
	if t.After(p.lastTime) {
		p.lastTime = t
	}
}

func slicesContains(list []string, s string) bool {
	return slices.Contains(list, s)
}

// lineTime extracts the lumberjack file-handler timestamp prefix
// ("2006/01/02 15:04:05") from a log line; zero time when unparseable
func lineTime(line string) time.Time {
	if len(line) < 19 {
		return time.Time{}
	}
	t, err := time.ParseInLocation("2006/01/02 15:04:05", line[:19], time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}
