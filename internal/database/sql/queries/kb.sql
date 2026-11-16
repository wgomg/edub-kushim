-- the offline lexical knowledge base reads and the install-time row writes. Every query is source-scoped:
-- a source's rows are replaced as one set and absence of a source is indistinguishable from a miss.

-- name: ListKBSources :many
SELECT * FROM kb_source ORDER BY source;

-- name: GetKBSource :one
SELECT * FROM kb_source WHERE source = @source;

-- name: InsertKBSource :exec
INSERT INTO kb_source (
    source, source_version, license, notice, upstream_url, upstream_sha256, converter_version,
    concept_count, lemma_count, edge_count
) VALUES (
    @source, @source_version, @license, @notice, @upstream_url, @upstream_sha256, @converter_version,
    @concept_count, @lemma_count, @edge_count
);

-- name: DeleteKBEdges :exec
DELETE FROM kb_edge WHERE source = @source;

-- name: DeleteKBLemmas :exec
DELETE FROM kb_lemma WHERE source = @source;

-- name: DeleteKBConcepts :exec
DELETE FROM kb_concept WHERE source = @source;

-- name: DeleteKBSource :exec
DELETE FROM kb_source WHERE source = @source;

-- name: InsertKBConcepts :exec
INSERT INTO kb_concept (source, concept_key, pos, gloss)
SELECT @source::text, r.concept_key, r.pos, r.gloss
FROM jsonb_to_recordset(@records::jsonb) AS r(
    concept_key text,
    pos text,
    gloss text
);

-- name: InsertKBLemmas :exec
INSERT INTO kb_lemma (source, concept_key, lemma_norm)
SELECT @source::text, r.concept_key, r.lemma_norm
FROM jsonb_to_recordset(@records::jsonb) AS r(
    concept_key text,
    lemma_norm text
);

-- name: InsertKBEdges :exec
INSERT INTO kb_edge (source, from_concept_key, to_concept_key, family, source_reltype, weight)
SELECT @source::text, r.from_concept_key, r.to_concept_key, r.family, r.source_reltype, r.weight
FROM jsonb_to_recordset(@records::jsonb) AS r(
    from_concept_key text,
    to_concept_key text,
    family text,
    source_reltype text,
    weight real
);

-- name: KBConceptsForLemma :many
SELECT c.source, s.source_version, c.concept_key, c.pos, c.gloss
FROM kb_lemma l
JOIN kb_concept c ON c.source = l.source AND c.concept_key = l.concept_key
JOIN kb_source s ON s.source = c.source
WHERE l.lemma_norm = @lemma
ORDER BY c.source, c.concept_key;

-- name: KBLemmasForConcepts :many
SELECT l.source, l.concept_key, l.lemma_norm
FROM kb_lemma l
JOIN (SELECT unnest(@sources::text[]) AS source, unnest(@concept_keys::text[]) AS concept_key) AS a
  ON l.source = a.source AND l.concept_key = a.concept_key
ORDER BY l.source, l.concept_key, l.lemma_norm;

-- name: KBEdgesFromConcepts :many
SELECT e.source, s.source_version, e.from_concept_key, e.to_concept_key, e.family, e.source_reltype,
       e.weight
FROM kb_edge e
JOIN kb_source s ON s.source = e.source
JOIN (SELECT unnest(@sources::text[]) AS source, unnest(@concept_keys::text[]) AS concept_key) AS a
  ON e.source = a.source AND e.from_concept_key = a.concept_key
WHERE e.family = ANY(@families::text[])
ORDER BY e.source, e.from_concept_key, e.family, e.to_concept_key;

-- name: KBEdgesBetweenConcepts :many
-- both directions of the pair are probed, so a caller sees the edge orientation the source carries; the
-- UNION collapses an edge twice when the two labels share an anchor
SELECT e.source, s.source_version, e.from_concept_key, e.to_concept_key, e.family, e.source_reltype,
       e.weight
FROM kb_edge e
JOIN kb_source s ON s.source = e.source
JOIN (SELECT unnest(sqlc.arg(a_sources)::text[]) AS source, unnest(sqlc.arg(a_keys)::text[]) AS concept_key) AS a
  ON e.source = a.source AND e.from_concept_key = a.concept_key
JOIN (SELECT unnest(sqlc.arg(b_sources)::text[]) AS source, unnest(sqlc.arg(b_keys)::text[]) AS concept_key) AS b
  ON e.source = b.source AND e.to_concept_key = b.concept_key
WHERE e.family = ANY(sqlc.arg(families)::text[])
UNION
SELECT e.source, s.source_version, e.from_concept_key, e.to_concept_key, e.family, e.source_reltype,
       e.weight
FROM kb_edge e
JOIN kb_source s ON s.source = e.source
JOIN (SELECT unnest(sqlc.arg(b_sources)::text[]) AS source, unnest(sqlc.arg(b_keys)::text[]) AS concept_key) AS b
  ON e.source = b.source AND e.from_concept_key = b.concept_key
JOIN (SELECT unnest(sqlc.arg(a_sources)::text[]) AS source, unnest(sqlc.arg(a_keys)::text[]) AS concept_key) AS a
  ON e.source = a.source AND e.to_concept_key = a.concept_key
WHERE e.family = ANY(sqlc.arg(families)::text[])
ORDER BY source, from_concept_key, family, to_concept_key;
