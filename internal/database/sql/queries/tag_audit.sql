-- name: GetTagAuditSweep :one
SELECT value FROM system_signal WHERE name = 'tag_audit_sweep';

-- name: SetTagAuditSweep :exec
UPDATE system_signal
SET value = sqlc.arg(sweep_count), modified_at = CURRENT_TIMESTAMP
WHERE name = 'tag_audit_sweep';

-- name: CountEligibleLiveDocuments :one
SELECT COUNT(*)
FROM document d
LEFT JOIN tag_audit_register r ON r.document_id = d.id
WHERE d.deleted_at IS NULL
  AND COALESCE(r.audit_count, 0) < @sweep_count;

-- name: SampleEligibleLiveDocumentIDs :many
SELECT d.id
FROM document d
LEFT JOIN tag_audit_register r ON r.document_id = d.id
WHERE d.deleted_at IS NULL
  AND COALESCE(r.audit_count, 0) < @sweep_count
ORDER BY random()
LIMIT @page_limit;

-- name: ListEligibleLiveDocumentsBelowTagFloor :many
SELECT d.id
FROM document d
LEFT JOIN document_tag dt ON dt.document_id = d.id
LEFT JOIN tag_audit_register r ON r.document_id = d.id
WHERE d.deleted_at IS NULL
  AND COALESCE(r.audit_count, 0) < @sweep_count
GROUP BY d.id
HAVING COUNT(dt.tag_id) < @tag_floor
ORDER BY d.id
LIMIT @page_limit;

-- name: UpsertTagAuditRegister :exec
INSERT INTO tag_audit_register (document_id, audit_count)
VALUES (@document_id, 1)
ON CONFLICT (document_id) DO UPDATE SET audit_count = tag_audit_register.audit_count + 1;
