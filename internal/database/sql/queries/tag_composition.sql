-- name: InsertTagCompositionEvent :exec
INSERT INTO tag_composition_event (document_id, candidate, parts, disposition, reason, criteria_version, model)
VALUES (@document_id, @candidate, @parts, @disposition, @reason, @criteria_version, @model);
