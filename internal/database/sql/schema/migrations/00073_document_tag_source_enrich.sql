-- +goose Up
-- the kb document-tag source is retired: containment attachments are enrichment attachments now, and the
-- tag row's origin carries the knowledge-base lineage instead, so legacy rows become plain enrich rows
UPDATE document_tag SET source = 'enrich' WHERE source = 'kb';

-- +goose Down
-- the kb marker is not reconstructible once normalized, so the down is a no-op
SELECT 1;
