//go:build ignore

// Command gen_attested_folds extracts the split seam's attested derivation table from the WordNet
// 3.0 database and writes it as internal/tags/attested_folds.txt, the asset the seam embeds.
//
// The table holds denotation-preserving adjective→noun pairs drawn from the pertainym relation of
// relational adjectives (the adj.pert lexicographer class): exactly the class where the adjective
// and the noun denote the same thing, so AnchoredFold can fold on attestation rather than on shape.
// The seam still anchors each attested base against the promoted store view and treats two distinct
// store hits as ambiguity, so the asset only needs the relation, not the store.
//
// Run it from the repository root; it downloads the pinned archive when -dict is empty and parses
// the extracted files otherwise:
//
//	go run internal/tags/gen_attested_folds.go -out internal/tags/attested_folds.txt
//	go run internal/tags/gen_attested_folds.go -dict /path/to/wn3.0/dict -out internal/tags/attested_folds.txt
//
// The committed asset is the source of truth; regeneration is manual and rare.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultArchiveURL = "https://wordnetcode.princeton.edu/3.0/WNdb-3.0.tar.gz"

const wndbArchiveSHA256 = "658b1ba191f5f98c2e9bae3e25c186013158f30ef779f191d2a44e5d25046dc8"

// wndbLicense is the WordNet 3.0 copyright and permission notice the WNDB license requires to
// accompany the database and any derived copy; it is emitted in the asset's header.
const wndbLicense = `WordNet 3.0 Copyright 2006 by Princeton University. All rights reserved.
THIS SOFTWARE AND DATABASE IS PROVIDED "AS IS" AND PRINCETON UNIVERSITY MAKES NO REPRESENTATIONS
OR WARRANTIES, EXPRESS OR IMPLIED. BY WAY OF EXAMPLE, BUT NOT LIMITATION, PRINCETON UNIVERSITY
MAKES NO REPRESENTATIONS OR WARRANTIES OF MERCHANTABILITY OR FITNESS FOR ANY PARTICULAR PURPOSE
OR THAT THE USE OF THE LICENSED SOFTWARE, DATABASE OR DOCUMENTATION WILL NOT INFRINGE ANY THIRD
PARTY PATENTS, COPYRIGHTS, TRADEMARKS OR OTHER RIGHTS. The name of Princeton University or
Princeton may not be used in advertising or publicity pertaining to distribution of the software
and/or database.`

func main() {
	dictDir := flag.String("dict", "", "path to an extracted WordNet database dict directory; empty downloads the archive")
	archiveURL := flag.String("url", defaultArchiveURL, "WordNet database archive to download when -dict is empty")
	out := flag.String("out", "internal/tags/attested_folds.txt", "output asset path")
	flag.Parse()

	var (
		adjData, nounData []byte
		source            string
	)
	if *dictDir != "" {
		var err error
		adjData, err = os.ReadFile(filepath.Join(*dictDir, "data.adj"))
		if err != nil {
			fatal(err)
		}
		nounData, err = os.ReadFile(filepath.Join(*dictDir, "data.noun"))
		if err != nil {
			fatal(err)
		}
		source = "extracted WordNet database at " + *dictDir
	} else {
		var err error
		adjData, nounData, source, err = download(*archiveURL)
		if err != nil {
			fatal(err)
		}
	}

	pairs := extractPertainyms(adjData, nounData)
	if len(pairs) == 0 {
		fatal(fmt.Errorf("extraction produced no pairs"))
	}

	if err := writeAsset(*out, pairs, source); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %d attested pairs to %s\n", len(pairs), *out)
}

// download fetches the archive and returns the two database files plus the provenance line: the
// archive URL and its checksum, pinned in the asset header so a later regeneration is auditable.
func download(url string) (adj, noun []byte, source string, err error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return nil, nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, "", fmt.Errorf("download %s: %s", url, resp.Status)
	}
	archive, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, "", err
	}
	sum := sha256.Sum256(archive)
	if hex.EncodeToString(sum[:]) != wndbArchiveSHA256 {
		return nil, nil, "", fmt.Errorf("archive %s sha256 %s does not match the pinned %s",
			url, hex.EncodeToString(sum[:]), wndbArchiveSHA256)
	}

	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, nil, "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, "", err
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, nil, "", err
		}
		switch {
		case strings.HasSuffix(hdr.Name, "/data.adj"):
			adj = data
		case strings.HasSuffix(hdr.Name, "/data.noun"):
			noun = data
		}
	}
	if adj == nil || noun == nil {
		return nil, nil, "", fmt.Errorf("archive %s lacks data.adj/data.noun", url)
	}
	return adj, noun, fmt.Sprintf("%s (sha256 %s)", url, hex.EncodeToString(sum[:])), nil
}

// extractPertainyms parses both database files and returns the sorted, deduplicated
// adjective→noun pairs the adj.pert pertainym relation attests, both sides single lowercase-ASCII
// tokens. The target index spans the adjective and noun files because a pertainym may point into
// either part of speech.
func extractPertainyms(adjData, nounData []byte) []string {
	targets := make(map[string][]string)
	collectWords(nounData, targets)
	collectWords(adjData, targets)

	pairs := make(map[string]struct{})
	for _, synset := range parseSynsets(adjData) {
		if synset.lexFile != adjectivePertLexFile {
			continue
		}
		bases := make([]string, 0, len(synset.words))
		for _, word := range synset.words {
			if clean, ok := singleToken(word); ok {
				bases = append(bases, clean)
			}
		}
		if len(bases) == 0 {
			continue
		}
		for _, ptr := range synset.pointers {
			if ptr.symbol != pertainymPointer {
				continue
			}
			for _, target := range targets[ptr.offset] {
				if clean, ok := singleToken(target); ok {
					for _, base := range bases {
						pairs[base+" "+clean] = struct{}{}
					}
				}
			}
		}
	}

	out := make([]string, 0, len(pairs))
	for pair := range pairs {
		out = append(out, pair)
	}
	sort.Strings(out)
	return out
}

// collectWords indexes every synset's lemmas by its 8-digit offset, so pointer targets resolve
// across both files.
func collectWords(data []byte, targets map[string][]string) {
	for _, synset := range parseSynsets(data) {
		if len(synset.words) > 0 {
			targets[synset.offset] = append(targets[synset.offset], synset.words...)
		}
	}
}

// adjectivePertLexFile is the lexicographer file number of adj.pert, the relational-adjective class.
const adjectivePertLexFile = 1

// pertainymPointer is the WNDB lexical pointer symbol that marks a pertainym relation.
const pertainymPointer = `\`

type synset struct {
	offset   string
	lexFile  int
	words    []string
	pointers []pointer
}

type pointer struct {
	symbol string
	offset string
}

// parseSynsets reads the synset entries of one WNDB data file line by line, ignoring the header
// license block: a synset line begins with an 8-digit zero-padded offset.
func parseSynsets(data []byte) []synset {
	var out []synset
	for _, line := range strings.Split(string(data), "\n") {
		offset := synsetOffset(line)
		if offset == "" {
			continue
		}
		body := line
		if i := strings.IndexByte(line, '|'); i >= 0 {
			body = line[:i]
		}
		fields := strings.Fields(body)
		if len(fields) < 4 {
			continue
		}
		lexFile, err := strconv.ParseInt(fields[1], 16, 32)
		if err != nil {
			continue
		}
		wCnt, err := strconv.ParseInt(fields[3], 16, 32)
		if err != nil {
			continue
		}
		idx := 4
		s := synset{offset: offset, lexFile: int(lexFile)}
		for i := 0; i < int(wCnt) && idx+1 < len(fields); i++ {
			s.words = append(s.words, fields[idx])
			idx += 2
		}
		if idx >= len(fields) {
			out = append(out, s)
			continue
		}
		pCnt, err := strconv.ParseInt(fields[idx], 16, 32)
		idx++
		if err != nil {
			out = append(out, s)
			continue
		}
		for i := 0; i < int(pCnt) && idx+3 < len(fields); i++ {
			s.pointers = append(s.pointers, pointer{symbol: fields[idx], offset: fields[idx+1]})
			idx += 4
		}
		out = append(out, s)
	}
	return out
}

// synsetOffset returns the 8-digit offset a synset line starts with, or "" for header and blank
// lines.
func synsetOffset(line string) string {
	if len(line) < 8 {
		return ""
	}
	offset := line[:8]
	for i := 0; i < 8; i++ {
		if offset[i] < '0' || offset[i] > '9' {
			return ""
		}
	}
	return offset
}

// singleToken reports whether a WNDB lemma is one token eligible for the seam table, stripping
// the adjective's position marker (`able(p)` → `able`) and lowercasing it: WNDB preserves the
// case of proper-noun-derived lemmas (Russian, Hungarian) while the store's tag namespace is
// lowercase. Multiword lemmas (underscored), punctuation, and non-ASCII lemmas are rejected.
func singleToken(lemma string) (string, bool) {
	if i := strings.IndexByte(lemma, '('); i >= 0 {
		lemma = lemma[:i]
	}
	if lemma == "" || strings.ContainsRune(lemma, '_') {
		return "", false
	}
	lemma = strings.ToLower(lemma)
	for i := 0; i < len(lemma); i++ {
		if lemma[i] < 'a' || lemma[i] > 'z' {
			return "", false
		}
	}
	return lemma, true
}

// writeAsset writes the header and the sorted pairs; the header carries the provenance and the
// WNDB notice the license requires.
func writeAsset(path string, pairs []string, source string) error {
	var b strings.Builder
	b.WriteString("# Attested adjective->noun folds for the split seam (AnchoredFold).\n")
	b.WriteString("# Generated by internal/tags/gen_attested_folds.go from the WordNet 3.0 database:\n")
	b.WriteString("# ")
	b.WriteString(source)
	b.WriteByte('\n')
	b.WriteString("# Extraction: pertainym (`\\`) pointers of adjective synsets in the adj.pert lexicographer\n")
	b.WriteString("# file; both sides kept only when a single lowercase-ASCII token. Regeneration is manual.\n")
	b.WriteString("#\n")
	for line := range strings.SplitSeq(wndbLicense, "\n") {
		b.WriteString("# ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString("\n")
	for _, pair := range pairs {
		b.WriteString(pair)
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gen_attested_folds:", err)
	os.Exit(1)
}
