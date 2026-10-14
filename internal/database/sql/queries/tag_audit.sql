-- name: GetTagAuditSweep :one
SELECT sweep_count FROM tag_audit_sweep WHERE id = 1;

-- name: BumpTagAuditSweep :exec
UPDATE tag_audit_sweep SET sweep_count = sweep_count + 1 WHERE id = 1;

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
