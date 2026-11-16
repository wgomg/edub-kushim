#!/usr/bin/env python3
"""Generate the offline lexical knowledge base artifacts consumed by internal/kb.

The script converts the pinned upstream releases into the artifact format the installer understands: one
gzipped tar per source with a manifest.json plus concepts.tsv, lemmas.tsv and edges.tsv. It is standard
library only and runs manually by the operator, the same standing as the other scripts in this directory.

The artifact format is the contract between this script and internal/kb: the manifest carries the provenance,
the license and the row counts, and the installer refuses an artifact without them. Any change to the mapping
or the filters must bump CONVERTER_VERSION so installed stores converge on the next ensure.

Usage:
    python3 scripts/gen_kb_artifacts.py --source oewn
    python3 scripts/gen_kb_artifacts.py --source conceptnet
    python3 scripts/gen_kb_artifacts.py --all
"""

import argparse
import datetime
import gzip
import hashlib
import json
import os
import shutil
import sys
import tarfile
import tempfile
import urllib.request
import xml.etree.ElementTree as ET

CONVERTER_VERSION = "1"

OEWN = {
    "source": "oewn",
    "source_version": "2025",
    "upstream_url": "https://en-word.net/static/english-wordnet-2025.xml.gz",
    "upstream_sha256": "9ca6d1dcb75f822fdd66617f7d9da48142ace38dd544d6ad5e2feca1674ad3fe",
    "upstream_filename": "english-wordnet-2025.xml.gz",
    "license": "CC-BY-4.0",
    "artifact_filename": "oewn-2025.tar.gz",
    "notice": (
        "Open English WordNet 2025 (https://en-word.net), Copyright 2025 by the Open English WordNet team. "
        "Distributed under the Creative Commons Attribution 4.0 International License (CC BY 4.0), "
        "https://creativecommons.org/licenses/by/4.0/. "
        "This artifact is a converted derivative of that release; the conversion keeps a subset of relations "
        "and normalizes lemmas (see the filters field). "
        "It incorporates material from WordNet 3.1, Copyright 2011 by Princeton University. All rights "
        "reserved. "
        "Permission to use, copy, modify and distribute this software and database and its documentation for "
        "any purpose and without fee or royalty is hereby granted, provided that you agree to comply with the "
        "following copyright notice and statements, including the disclaimer, and that the same appear on ALL "
        "copies of the software, database and documentation, including modifications that you make for "
        "internal use or for distribution. "
        'THIS SOFTWARE AND DATABASE IS PROVIDED "AS IS" AND PRINCETON UNIVERSITY MAKES NO REPRESENTATIONS OR '
        "WARRANTIES, EXPRESS OR IMPLIED. BY WAY OF EXAMPLE, BUT NOT LIMITATION, PRINCETON UNIVERSITY MAKES NO "
        "REPRESENTATIONS OR WARRANTIES OF MERCHANT-ABILITY OR FITNESS FOR ANY PARTICULAR PURPOSE OR THAT THE "
        "USE OF THE LICENSED SOFTWARE, DATABASE OR DOCUMENTATION WILL NOT INFRINGE ANY THIRD PARTY PATENTS, "
        "COPYRIGHTS, TRADEMARKS OR OTHER RIGHTS. "
        "The name of Princeton University or Princeton may not be used in advertising or publicity pertaining "
        "to distribution of the software and/or database. Title to copyright in this software, database and "
        "any associated documentation shall at all times remain with Princeton University and LICENSEE agrees "
        "to preserve same. "
        "Canonical citation: John P. McCrae, Alexandre Rademaker, Ewa Rudnicka and Francis Bond (2019), "
        "English WordNet 2019 - An Open-Source WordNet for English, Proceedings of the 10th Global WordNet "
        "Conference."
    ),
    "filters": (
        "Kept synset relations hypernym (broader), hyponym (narrower), instance_hypernym and "
        "instance_hyponym (instance), the meronym/holonym member, part and substance relations (part), and "
        "similar (similar). Kept sense relations antonym (antonym) and similar (similar). Dropped derivation, "
        "pertainym, also, attribute, domain, exemplifies, entailment, causation, participle and other "
        "lexical relations. The gloss is the synset's first definition. Lemmas are lowercased, underscores "
        "become spaces, whitespace is collapsed, and duplicate rows are removed. Synonymy (exact) is not "
        "materialized: the store derives it from lemmas sharing one concept."
    ),
}

CONCEPTNET = {
    "source": "conceptnet",
    "source_version": "5.7.0-en",
    "upstream_url": "https://s3.amazonaws.com/conceptnet/downloads/2019/edges/conceptnet-assertions-5.7.0.csv.gz",
    "upstream_sha256": "accd65fe94038584295574ddc26e1500c1919c8c4532bf771811cafd0948af7e",
    "upstream_filename": "conceptnet-assertions-5.7.0.csv.gz",
    "license": "CC-BY-SA-4.0",
    "artifact_filename": "conceptnet-5.7.0-en.tar.gz",
    "notice": (
        "ConceptNet 5.7.0, Copyright 2019 by the Commonsense Computing Initiative (Robyn Speer, Joshua Chin "
        "and Catherine Havasi). The complete graph is distributed by its publisher under the Creative Commons "
        "Attribution-ShareAlike 4.0 International License (CC BY-SA 4.0), "
        "https://creativecommons.org/licenses/by-sa/4.0/. This artifact is a converted derivative of that "
        "release and is distributed under CC BY-SA 4.0 as a whole. Changes: it keeps English word nodes only; "
        "it retains only the Synonym, SimilarTo, Antonym, DistinctFrom, IsA, PartOf and HasA relations; node "
        "URIs are reduced to their word form, lemmas are lowercased with underscores as spaces, terms with "
        "more than three words or characters outside letters, digits, spaces and hyphens are dropped, "
        "self-relations left behind by sense collapsing are dropped, and duplicate rows are removed. "
        "Cite: Robyn Speer, Joshua Chin and Catherine Havasi (2017), ConceptNet 5.5: An Open Multilingual "
        "Graph of General Knowledge, Proceedings of the AAAI Conference on Artificial Intelligence 31(1)."
    ),
    "filters": (
        "Requires both edge endpoints under /c/en/. Node URIs are reduced to their word form, dropping POS and "
        "sense suffixes. Terms are lowercased, underscores become spaces and whitespace is collapsed. Terms "
        "with more than three tokens or with characters outside letters, digits, spaces and hyphens are "
        "dropped. Kept relations: Synonym (exact), SimilarTo (similar), Antonym (antonym), DistinctFrom "
        "(distinct), IsA (instance), PartOf and HasA (part). Everything else is excluded, including RelatedTo, "
        "FormOf, DerivedFrom, UsedFor and ExternalURL. Self-relations left behind by sense collapsing are "
        "dropped. Duplicate (start, end, relation) rows are removed and the first weight is kept verbatim."
    ),
}

SOURCES = {"oewn": OEWN, "conceptnet": CONCEPTNET}

OEWN_SYNSET_RELATIONS = {
    "hypernym": "broader",
    "hyponym": "narrower",
    "instance_hypernym": "instance",
    "instance_hyponym": "instance",
    "mero_member": "part",
    "mero_part": "part",
    "mero_substance": "part",
    "holo_member": "part",
    "holo_part": "part",
    "holo_substance": "part",
    "similar": "similar",
}

OEWN_SENSE_RELATIONS = {
    "antonym": "antonym",
    "similar": "similar",
}

CONCEPTNET_RELATIONS = {
    "/r/Synonym": "exact",
    "/r/SimilarTo": "similar",
    "/r/Antonym": "antonym",
    "/r/DistinctFrom": "distinct",
    "/r/IsA": "instance",
    "/r/PartOf": "part",
    "/r/HasA": "part",
}


def clean_text(text):
    """Collapse a free-text field to one TSV-safe line."""
    return " ".join(text.replace("\t", " ").replace("\n", " ").split())


def normalize_lemma(text):
    """Apply the store's house form: lowercase, underscores as spaces, collapsed whitespace."""
    return " ".join(text.replace("_", " ").strip().lower().split())


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def fetch_upstream(spec, cache_dir):
    """Return the pinned upstream file, downloading it once into the cache and verifying its hash."""
    os.makedirs(cache_dir, exist_ok=True)
    path = os.path.join(cache_dir, spec["upstream_filename"])

    if os.path.exists(path):
        actual = sha256_file(path)
        if actual != spec["upstream_sha256"]:
            raise SystemExit(
                f"cached upstream {path} has sha256 {actual}, pin holds {spec['upstream_sha256']}; "
                "remove it and rerun to download the pinned release"
            )
        print(f"using cached upstream {path}")
        return path

    print(f"downloading {spec['upstream_url']}")
    tmp = path + ".part"
    digest = hashlib.sha256()
    size = 0
    with urllib.request.urlopen(spec["upstream_url"]) as response, open(tmp, "wb") as out:
        while True:
            chunk = response.read(1024 * 1024)
            if not chunk:
                break
            digest.update(chunk)
            out.write(chunk)
            size += len(chunk)
            if size % (64 * 1024 * 1024) < 1024 * 1024:
                print(f"  {size // (1024 * 1024)} MiB", flush=True)

    actual = digest.hexdigest()
    if actual != spec["upstream_sha256"]:
        os.remove(tmp)
        raise SystemExit(
            f"downloaded upstream {spec['upstream_url']} has sha256 {actual}, pin holds "
            f"{spec['upstream_sha256']}"
        )
    os.replace(tmp, path)
    print(f"cached upstream {path} ({size} bytes)")
    return path


def parse_oewn(upstream_path):
    """Convert the WN-LMF release into the concept, lemma and edge rows."""
    concepts = {}
    lemma_rows = set()
    sense_to_synset = {}
    synset_edges = set()
    sense_edges = []

    # iterparse over the 89 MB document: children are cleared once their parent is consumed, so the parsed
    # tree stays bounded while the per-row sets hold the whole graph.
    with gzip.open(upstream_path, "rb") as f:
        for _, elem in ET.iterparse(f, events=("end",)):
            if elem.tag == "Synset":
                synset_id = elem.get("id")
                pos = elem.get("partOfSpeech") or ""
                gloss = ""
                for child in elem:
                    if child.tag == "Definition":
                        gloss = clean_text("".join(child.itertext()))
                        break
                if synset_id:
                    concepts[synset_id] = (pos, gloss)
                    for child in elem:
                        if child.tag != "SynsetRelation":
                            continue
                        family = OEWN_SYNSET_RELATIONS.get(child.get("relType"))
                        target = child.get("target")
                        if family and target:
                            synset_edges.add((synset_id, target, family, child.get("relType")))
                elem.clear()
            elif elem.tag == "LexicalEntry":
                lemma = ""
                for child in elem:
                    if child.tag == "Lemma":
                        lemma = normalize_lemma(child.get("writtenForm") or "")
                        break
                for child in elem:
                    if child.tag != "Sense":
                        continue
                    synset_id = child.get("synset")
                    if not synset_id:
                        continue
                    sense_to_synset[child.get("id")] = synset_id
                    if lemma:
                        lemma_rows.add((synset_id, lemma))
                    for relation in child:
                        if relation.tag != "SenseRelation":
                            continue
                        family = OEWN_SENSE_RELATIONS.get(relation.get("relType"))
                        target = relation.get("target")
                        if family and target:
                            sense_edges.append((synset_id, target, family, relation.get("relType")))
                elem.clear()

    edges = set()
    dangling = []
    for from_key, to_key, family, reltype in synset_edges:
        if from_key in concepts and to_key in concepts:
            edges.add((from_key, to_key, family, reltype))
        else:
            dangling.append((from_key, to_key, reltype))
    for from_key, target_sense, family, reltype in sense_edges:
        to_key = sense_to_synset.get(target_sense)
        if to_key in concepts:
            edges.add((from_key, to_key, family, reltype))
        else:
            dangling.append((from_key, target_sense, reltype))

    if dangling:
        sample = ", ".join("/".join(item) for item in dangling[:3])
        raise SystemExit(f"OEWN carries {len(dangling)} dangling relation targets, e.g. {sample}")
    if not concepts or not lemma_rows or not edges:
        raise SystemExit("OEWN conversion produced an empty row-set")

    concept_rows = sorted((key, pos, gloss) for key, (pos, gloss) in concepts.items())
    edge_rows = sorted((from_key, to_key, family, reltype, "") for from_key, to_key, family, reltype in edges)
    print(f"oewn: {len(concept_rows)} concepts, {len(lemma_rows)} lemmas, {len(edge_rows)} edges")
    return concept_rows, sorted(lemma_rows), edge_rows


def conceptnet_node(uri):
    """Return the English word node of a ConceptNet URI, dropping POS and sense suffixes."""
    if not uri.startswith("/c/en/"):
        return None
    parts = uri.split("/")
    if len(parts) < 4 or not parts[3]:
        return None
    return "/c/en/" + parts[3]


def conceptnet_term(node):
    """Return the normalized term a node URI names, or None when the plan's token filter rejects it."""
    raw = node[len("/c/en/") :]
    lemma = normalize_lemma(raw)
    tokens = lemma.split()
    if not 1 <= len(tokens) <= 3:
        return None
    if not any(ch.isalnum() for ch in lemma):
        return None
    if not all(ch.isalnum() or ch in " -" for ch in lemma):
        return None
    return lemma


def parse_conceptnet(upstream_path):
    """Convert the assertions TSV into the concept, lemma and edge rows."""
    concepts = {}  # node URI -> normalized lemma
    edges = {}  # (from, to, family, reltype) -> weight text, first wins

    with gzip.open(upstream_path, "rt", encoding="utf-8", newline="") as f:
        for line_number, line in enumerate(f, start=1):
            fields = line.rstrip("\r\n").split("\t")
            if len(fields) < 5:
                raise SystemExit(f"conceptnet line {line_number}: {len(fields)} columns, want 5")
            reltype = fields[1]
            family = CONCEPTNET_RELATIONS.get(reltype)
            if not family:
                continue

            start_node = conceptnet_node(fields[2])
            end_node = conceptnet_node(fields[3])
            if not start_node or not end_node or start_node == end_node:
                continue
            start_term = conceptnet_term(start_node)
            end_term = conceptnet_term(end_node)
            if not start_term or not end_term:
                continue

            key = (start_node, end_node, family, reltype)
            if key in edges:
                continue
            payload = json.loads(fields[4])
            weight = payload.get("weight")
            if weight is None:
                weight_text = ""
            elif isinstance(weight, bool) or not isinstance(weight, (int, float)):
                raise SystemExit(f"conceptnet line {line_number}: invalid weight {weight!r}")
            else:
                weight_text = repr(weight)

            concepts.setdefault(start_node, start_term)
            concepts.setdefault(end_node, end_term)
            edges[key] = weight_text

            if line_number % 5000000 == 0:
                print(f"  read {line_number} lines, {len(edges)} kept edges", flush=True)

    if not concepts or not edges:
        raise SystemExit("ConceptNet conversion produced an empty row-set")

    concept_rows = sorted((node, "", "") for node in concepts)
    lemma_rows = sorted((node, lemma) for node, lemma in concepts.items())
    edge_rows = sorted(
        (from_key, to_key, family, reltype, weight)
        for (from_key, to_key, family, reltype), weight in edges.items()
    )
    print(f"conceptnet: {len(concept_rows)} concepts, {len(lemma_rows)} lemmas, {len(edge_rows)} edges")
    return concept_rows, lemma_rows, edge_rows


def write_tsv(path, rows, columns):
    with open(path, "w", encoding="utf-8", newline="\n") as f:
        for row in rows:
            if len(row) != columns:
                raise SystemExit(f"{path}: row has {len(row)} columns, want {columns}: {row!r}")
            f.write("\t".join(row) + "\n")


def add_file(tar, path, arcname):
    info = tar.gettarinfo(path, arcname=arcname)
    info.mtime = 0
    info.uid = info.gid = 0
    info.uname = info.gname = ""
    with open(path, "rb") as f:
        tar.addfile(info, f)


def write_artifact(out_dir, spec, concept_rows, lemma_rows, edge_rows, upstream_path):
    """Package the rows into the published artifact, with stable tar metadata for reproducible archives."""
    generated_at = datetime.datetime.now(datetime.timezone.utc).replace(microsecond=0)
    manifest = {
        "source": spec["source"],
        "source_version": spec["source_version"],
        "upstream_url": spec["upstream_url"],
        "upstream_sha256": spec["upstream_sha256"],
        "license": spec["license"],
        "notice": spec["notice"],
        "converter_version": CONVERTER_VERSION,
        "filters": spec["filters"],
        "counts": {
            "concepts": len(concept_rows),
            "lemmas": len(lemma_rows),
            "edges": len(edge_rows),
        },
        "generated_at": generated_at.isoformat().replace("+00:00", "Z"),
    }

    os.makedirs(out_dir, exist_ok=True)
    staging = tempfile.mkdtemp(prefix=f"{spec['source']}-", dir=out_dir)
    try:
        concepts_path = os.path.join(staging, "concepts.tsv")
        lemmas_path = os.path.join(staging, "lemmas.tsv")
        edges_path = os.path.join(staging, "edges.tsv")
        manifest_path = os.path.join(staging, "manifest.json")
        write_tsv(concepts_path, concept_rows, 3)
        write_tsv(lemmas_path, lemma_rows, 2)
        write_tsv(edges_path, edge_rows, 5)
        with open(manifest_path, "w", encoding="utf-8", newline="\n") as f:
            json.dump(manifest, f, ensure_ascii=False, indent=2)
            f.write("\n")

        artifact_path = os.path.join(out_dir, spec["artifact_filename"])
        tmp_path = artifact_path + ".part"
        with open(tmp_path, "wb") as raw:
            with gzip.GzipFile(fileobj=raw, mode="wb", mtime=0, compresslevel=9) as gz:
                with tarfile.open(fileobj=gz, mode="w", format=tarfile.PAX_FORMAT) as tar:
                    for path, arcname in (
                        (manifest_path, "manifest.json"),
                        (concepts_path, "concepts.tsv"),
                        (lemmas_path, "lemmas.tsv"),
                        (edges_path, "edges.tsv"),
                    ):
                        add_file(tar, path, arcname)
        os.replace(tmp_path, artifact_path)
    finally:
        shutil.rmtree(staging, ignore_errors=True)

    digest = sha256_file(artifact_path)
    print(f"wrote {artifact_path} ({os.path.getsize(artifact_path)} bytes, sha256 {digest})")
    print(f"upstream {upstream_path} sha256 {spec['upstream_sha256']}")
    return artifact_path, digest


def generate(source, out_dir):
    spec = SOURCES[source]
    cache_dir = os.path.join(out_dir, "upstream")
    upstream_path = fetch_upstream(spec, cache_dir)
    if source == "oewn":
        concept_rows, lemma_rows, edge_rows = parse_oewn(upstream_path)
    else:
        concept_rows, lemma_rows, edge_rows = parse_conceptnet(upstream_path)
    write_artifact(out_dir, spec, concept_rows, lemma_rows, edge_rows, upstream_path)


def main(argv):
    parser = argparse.ArgumentParser(description="Generate the offline lexical knowledge base artifacts.")
    parser.add_argument(
        "--source",
        action="append",
        choices=sorted(SOURCES),
        help="source to convert (repeatable)",
    )
    parser.add_argument("--all", action="store_true", help="convert every pinned source")
    parser.add_argument("--out", default="build/kb", help="output directory (default: build/kb)")
    args = parser.parse_args(argv)

    selected = []
    for source in args.source or []:
        if source not in selected:
            selected.append(source)
    if args.all:
        for source in SOURCES:
            if source not in selected:
                selected.append(source)
    if not selected:
        parser.error("choose at least one --source or --all")

    for source in selected:
        generate(source, args.out)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
