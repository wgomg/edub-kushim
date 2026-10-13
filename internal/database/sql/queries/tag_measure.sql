-- name: SnapshotNow :one
SELECT now()::timestamptz AS snapshot_at;

-- name: ListTagsForMeasure :many
SELECT id, name, promoted_at FROM tag ORDER BY id;

-- name: TagLivePairOverlaps :many
SELECT a.tag_id AS tag_a, b.tag_id AS tag_b, COUNT(*) AS document_count
FROM document_tag a
JOIN document_tag b ON b.document_id = a.document_id AND b.tag_id > a.tag_id
JOIN document d ON d.id = a.document_id
WHERE d.deleted_at IS NULL
GROUP BY a.tag_id, b.tag_id;

-- name: TagCompoundReconstructibility :many
WITH compound_parts AS (
    SELECT unnest(@compound_ids::bigint[]) AS compound_id,
           unnest(@part_ids::bigint[]) AS part_id
),
part_counts AS (
    SELECT compound_id, COUNT(DISTINCT part_id) AS part_count
    FROM compound_parts
    GROUP BY compound_id
),
carried AS (
    SELECT cp.compound_id, dt.document_id, COUNT(DISTINCT dt.tag_id) AS carried
    FROM compound_parts cp
    JOIN document_tag dt ON dt.tag_id = cp.part_id
    JOIN document d ON d.id = dt.document_id AND d.deleted_at IS NULL
    GROUP BY cp.compound_id, dt.document_id
)
SELECT carried.compound_id::bigint AS compound_id,
       COUNT(*) AS parts_intersection,
       COUNT(*) FILTER (WHERE dtc.document_id IS NOT NULL) AS both
FROM carried
JOIN part_counts pc ON pc.compound_id = carried.compound_id
LEFT JOIN document_tag dtc
       ON dtc.document_id = carried.document_id AND dtc.tag_id = carried.compound_id
WHERE carried.carried = pc.part_count
GROUP BY carried.compound_id;

-- name: InsertTagMeasurements :exec
INSERT INTO tag_measurement (run_id, snapshot_at, rule_set_version, operator, operands, numbers, state, caveats)
SELECT @run_id, @snapshot_at, @rule_set_version,
       r.operator, r.operands, r.numbers, r.state, r.caveats
FROM jsonb_to_recordset(@records::jsonb) AS r(
    operator text,
    operands text[],
    numbers jsonb,
    state text,
    caveats text[]
);

-- name: LatestTagMeasurementRun :one
SELECT run_id, snapshot_at, rule_set_version
FROM tag_measurement
ORDER BY snapshot_at DESC, id DESC
LIMIT 1;

-- name: TagMeasurementRecordsForRun :many
SELECT operator, operands, numbers, state, caveats
FROM tag_measurement
WHERE run_id = @run_id
ORDER BY id;
