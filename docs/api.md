# edub API

This reference is generated: the specification is derived from the Go source by `make api-spec`,
and this file is rendered from the specification by `make api-docs`. Do not edit either one by
hand; change the code and regenerate.

- API version: `4.1.0`
- Specification: [`internal/api/spec/openapi.yaml`](../internal/api/spec/openapi.yaml)

REST API of the edub document server: library browsing, search, tags, people, document types, trash, ingestion, task and batch monitoring, users, logs and the settings surface the web UI drives.

## Overview

Base URL: `http://localhost:3000` (Local edub server (default port; server.port in the config)). Every endpoint below is relative to it; the port comes from `server.port` in the config.

### Authentication

Every endpoint authenticates through the `Authorization` header, either with a session JWT minted by
`POST /api/v1/auth/login` or with a personal API key (`ek_` prefix) managed through the `/api/v1/me/api-key`
endpoints. Browser sessions also authenticate through the `edub_token` HttpOnly cookie, which the web UI
sets on login and clears on logout. `GET /health`, `POST /api/v1/auth/login`, `POST /api/v1/auth/logout`
and `GET /wizard/bootstrap` are public; everything else needs a credential. With `auth_enabled: false`
every request passes as an administrator.

Roles gate the operations:

- `viewer` — read-only access: documents, search, tags, people, document types, tasks, batches,
  the dashboard, the trash listing, the provisional tag queue and reconciliation report, audit flags
  and the sibling worklist.
- `editor` — everything a viewer sees, plus the mutations: document edits and deletion, tag, people
  and document-type changes, uploads and ingestion, trash restore and purge, orphaned and errored file
  handling, batch resume and cancel, re-enrichment, tag promotion and audit decisions.
- `admin` — everything an editor does, plus user management, API keys, LLM catalogs, logs and the
  settings surface (`/wizard/*`).

### Errors

Failure responses carry a JSON object with a single `error` field:

```json
{"error": "document not found"}
```

Some handlers answer through Go's `http.Error`, which writes the message as `text/plain`. The status
codes of each endpoint are listed below; `404` means the addressed row does not exist, `409` a
conflict with the current state, `503` an unavailable dependency (the tag matcher, a task handler).

### Pagination

Listing endpoints take `limit` and `offset`; each endpoint's parameter table states its defaults.
The `q` parameter, where present, is a name-prefix search.

## Endpoints

### Documents

#### `GET /api/v1/documents`

ListDocuments serves the paged document listing.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `limit` | query | integer | no | `50` |
| `offset` | query | integer | no | `0` |
| `sort_by` | query | string | no | `created_at` |
| `sort_order` | query | string | no | `desc` |

| Status | Body |
|---|---|
| 200 | array of [DocumentResponse](#documentresponse) |
| 500 | string |

#### `POST /api/v1/documents/batch-delete`

BatchDeleteDocuments soft-deletes many documents at once, reporting per-document failures.

Request body (required) (`application/json`): [BatchDeleteRequest](#batchdeleterequest)

| Status | Body |
|---|---|
| 400 | object |
| default | [BatchDeleteResult](#batchdeleteresult) |

#### `POST /api/v1/documents/batch-tags`

BatchAssignTags adds or removes tags over many documents, reporting per-document failures.

Request body (required) (`application/json`): [BatchTagRequest](#batchtagrequest)

| Status | Body |
|---|---|
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |
| default | [BatchTagResult](#batchtagresult) |

#### `POST /api/v1/documents/batch-type`

BatchSetDocumentType sets one document type over many documents.

Request body (required) (`application/json`): [BatchDocumentTypeRequest](#batchdocumenttyperequest)

| Status | Body |
|---|---|
| 400 | object |
| 404 | string |
| 409 | object |
| 500 | string |
| default | [BatchDocumentTypeResult](#batchdocumenttyperesult) |

#### `POST /api/v1/documents/download`

DownloadDocuments streams one document's file or a zip of several.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `Content-Type` | header | string | no | — |
| `document_ids` | query | string | no | — |

Request body (required) (`application/json`): [DocumentDownloadRequest](#documentdownloadrequest)

| Status | Body |
|---|---|
| 200 | string (binary) |
| 400 | object |
| 404 | string |
| 500 | object |

#### `GET /api/v1/documents/search`

SearchDocuments serves the free-text full-text search.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `q` | query | string | no | "" |
| `limit` | query | integer | no | `50` |
| `offset` | query | integer | no | `0` |

| Status | Body |
|---|---|
| 200 | array of [FTSDocumentResponse](#ftsdocumentresponse) |
| 400 | string |
| 500 | string |

#### `POST /api/v1/documents/search`

SearchDocumentsStructured serves the filter search with total count.

Request body (required) (`application/json`): [Filter](#filter)

| Status | Body |
|---|---|
| 200 | [SearchResponse](#searchresponse) |
| 400 | string |
| 500 | string |

#### `GET /api/v1/documents/{id}`

GetDocument serves one document's full view.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 200 | [DocumentResponse](#documentresponse) |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `PUT /api/v1/documents/{id}`

UpdateDocument updates a document's title, type, language and optional corrected text.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

Request body (required) (`application/json`): [DocumentUpdateRequest](#documentupdaterequest)

| Status | Body |
|---|---|
| 204 | no body |
| 400 | object |
| 404 | string |
| 409 | object |
| 500 | string |

#### `DELETE /api/v1/documents/{id}`

DeleteDocument soft-deletes one document into the trash.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 204 | no body |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `GET /api/v1/documents/{id}/file`

GetDocumentFile streams the stored file with its content type.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |
| `download` | query | string | no | — |

| Status | Body |
|---|---|
| 200 | string (binary) |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `POST /api/v1/documents/{id}/people`

AddDocumentPeople attaches one person to a document with a role.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

Request body (required) (`application/json`): [AddDocumentPeopleRequest](#adddocumentpeoplerequest)

| Status | Body |
|---|---|
| 204 | no body |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `DELETE /api/v1/documents/{id}/people`

RemoveDocumentPeople detaches one person role from a document.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

Request body (required) (`application/json`): [RemoveDocumentPeopleRequest](#removedocumentpeoplerequest)

| Status | Body |
|---|---|
| 204 | no body |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `POST /api/v1/documents/{id}/reenrich`

ReEnrich enqueues one document's re-enrichment.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 202 | object |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `POST /api/v1/documents/{id}/tags`

AddDocumentTag attaches one tag to a document.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

Request body (required) (`application/json`): [AddDocumentTagRequest](#adddocumenttagrequest)

| Status | Body |
|---|---|
| 204 | no body |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `DELETE /api/v1/documents/{id}/tags`

RemoveDocumentTag detaches one tag from a document.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

Request body (required) (`application/json`): [RemoveDocumentTagRequest](#removedocumenttagrequest)

| Status | Body |
|---|---|
| 204 | no body |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `GET /api/v1/documents/{id}/thumbnail`

GetDocumentThumbnail serves the document's thumbnail, 404 when there is none.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 200 | string (binary) |
| 400 | object |
| 404 | string |
| 409 | object |
| 500 | string |

#### `GET /api/v1/filter-languages`

FilterLanguages serves the distinct stored languages.

| Status | Body |
|---|---|
| 200 | array of string |
| 500 | string |

#### `GET /api/v1/supported-mime-types`

SupportedMimeTypes serves the formats the pipeline ingests.

| Status | Body |
|---|---|
| 200 | array of [MimeInfo](#mimeinfo) |

### Tags

#### `GET /api/v1/tags`

List serves the paged tag listing with a name-prefix search and document counts.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `q` | query | string | no | "" |
| `limit` | query | integer | no | `50` |
| `offset` | query | integer | no | `0` |

| Status | Body |
|---|---|
| 200 | [TagListResponse](#taglistresponse) |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `POST /api/v1/tags`

Create creates one tag from the JSON body.

Request body (required) (`application/json`): [CreateTagRequest](#createtagrequest)

| Status | Body |
|---|---|
| 200 | [TagResponse](#tagresponse) |
| 201 | [TagResponse](#tagresponse) |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |
| 503 | object |

#### `GET /api/v1/tags/audit/flags`

ListAuditFlags serves the paged open coverage flags.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `limit` | query | integer | no | `50` |
| `offset` | query | integer | no | `0` |

| Status | Body |
|---|---|
| 200 | [CoverageFlagListResponse](#coverageflaglistresponse) |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `POST /api/v1/tags/audit/flags/apply`

ApplyAuditFlag confirms a coverage flag, attaching the dimension to the document.

Request body (required) (`application/json`): [CoverageFlagActionRequest](#coverageflagactionrequest)

| Status | Body |
|---|---|
| 204 | no body |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `POST /api/v1/tags/audit/flags/create-value`

CreateAuditValue registers a missing flagged dimension through the normal tag-creation path: the value is born provisionally with pending identity evidence, and the fill lands on a later audit pass.

Request body (required) (`application/json`): [CreateAuditValueRequest](#createauditvaluerequest)

| Status | Body |
|---|---|
| 200 | object |
| 204 | no body |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |
| 503 | object |

#### `POST /api/v1/tags/audit/flags/dismiss`

DismissAuditFlag records the operator's not-applies outcome for a coverage flag.

Request body (required) (`application/json`): [CoverageFlagActionRequest](#coverageflagactionrequest)

| Status | Body |
|---|---|
| 204 | no body |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `POST /api/v1/tags/audit/reevaluate`

ReevaluateAudit enqueues a fresh completeness audit for the vocabulary.

| Status | Body |
|---|---|
| 202 | object |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `GET /api/v1/tags/deleted`

ListDeleted serves the paged deleted-tag records.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `q` | query | string | no | "" |
| `limit` | query | integer | no | `50` |
| `offset` | query | integer | no | `0` |

| Status | Body |
|---|---|
| 200 | [DeletedTagListResponse](#deletedtaglistresponse) |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `DELETE /api/v1/tags/deleted/{name}`

RestoreDeleted unblocks one deleted tag name.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `name` | path | string | yes | — |

| Status | Body |
|---|---|
| 200 | object |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |
| 503 | object |

#### `POST /api/v1/tags/identity/reevaluate`

ReevaluateIdentity enqueues a fresh identity sweep for the vocabulary.

| Status | Body |
|---|---|
| 202 | object |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `GET /api/v1/tags/provisional`

ListProvisional serves the paged provisional tag list with quality verdicts, pending identity counts, and identity holds.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `criteria` | query | array of string | no | — |
| `verdict` | query | string | no | "" |
| `limit` | query | integer | no | `50` |
| `offset` | query | integer | no | `0` |

| Status | Body |
|---|---|
| 200 | [ProvisionalTagListResponse](#provisionaltaglistresponse) |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `GET /api/v1/tags/reconciliation/report`

GetTagReconcileReport serves the latest completed reconcile task's report.

| Status | Body |
|---|---|
| 200 | [TagReconcileReportResponse](#tagreconcilereportresponse) |
| 404 | string |
| 500 | string |

#### `GET /api/v1/tags/siblings`

ListSiblings serves the latest measurement run's near-contained tag pairs for review.

Containment carries no automatic action, so the endpoint is read-only: nothing merges, holds, or demotes from these readings.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `limit` | query | integer | no | `50` |
| `offset` | query | integer | no | `0` |

| Status | Body |
|---|---|
| 200 | [SiblingListResponse](#siblinglistresponse) |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `PUT /api/v1/tags/{id}`

Update renames one tag.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

Request body (required) (`application/json`): [UpdateTagRequest](#updatetagrequest)

| Status | Body |
|---|---|
| 200 | [TagResponse](#tagresponse) or object |
| 400 | object |
| 404 | string |
| 409 | object |
| 500 | string |
| 503 | object |

#### `DELETE /api/v1/tags/{id}`

Delete removes a tag and blocks its name.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 200 | object |
| 204 | no body |
| 400 | object |
| 404 | string |
| 409 | object |
| 500 | string |
| 503 | object |

#### `POST /api/v1/tags/{id}/demote`

DemoteTag withdraws a tag's standing at an operator's request.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 200 | object |
| 204 | no body |
| 400 | object |
| 404 | string |
| 409 | object |
| 500 | string |

#### `POST /api/v1/tags/{id}/promote`

PromoteTag promotes one provisional tag, recording the reason.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 200 | object |
| 204 | no body |
| 400 | object |
| 404 | string |
| 409 | object |
| 500 | string |

### People

#### `GET /api/v1/people`

List serves the paged people listing with document counts.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `q` | query | string | no | "" |
| `limit` | query | integer | no | `50` |
| `offset` | query | integer | no | `0` |

| Status | Body |
|---|---|
| 200 | [PersonListResponse](#personlistresponse) |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `POST /api/v1/people`

Create creates one person from the JSON body.

Request body (required) (`application/json`): [CreatePersonRequest](#createpersonrequest)

| Status | Body |
|---|---|
| 201 | [PersonResponse](#personresponse) |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `GET /api/v1/people-types`

ListPeopleTypes serves the people-type taxonomy with paging.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `q` | query | string | no | "" |
| `limit` | query | integer | no | `50` |
| `offset` | query | integer | no | `0` |

| Status | Body |
|---|---|
| 200 | array of [PeopleTypeResponse](#peopletyperesponse) |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `POST /api/v1/people-types`

CreatePeopleType creates one people type from the JSON body.

Request body (required) (`application/json`): [CreatePeopleTypeRequest](#createpeopletyperequest)

| Status | Body |
|---|---|
| 201 | [PeopleTypeResponse](#peopletyperesponse) |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `PUT /api/v1/people-types/{id}`

UpdatePeopleType renames one people type.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

Request body (required) (`application/json`): [UpdatePeopleTypeRequest](#updatepeopletyperequest)

| Status | Body |
|---|---|
| 200 | [PeopleTypeResponse](#peopletyperesponse) |
| 400 | object |
| 404 | string |
| 409 | object |
| 500 | string |

#### `DELETE /api/v1/people-types/{id}`

DeletePeopleType removes one people type.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 204 | no body |
| 400 | object |
| 404 | string |
| 409 | object |
| 500 | string |

#### `PUT /api/v1/people/{id}`

Update renames one person.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

Request body (required) (`application/json`): [UpdatePersonRequest](#updatepersonrequest)

| Status | Body |
|---|---|
| 200 | [PersonResponse](#personresponse) |
| 400 | object |
| 404 | string |
| 409 | object |
| 500 | string |

#### `DELETE /api/v1/people/{id}`

Delete removes one person.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 204 | no body |
| 400 | object |
| 404 | string |
| 409 | object |
| 500 | string |

### Document types

#### `GET /api/v1/document-types`

List serves the paged document types with document counts.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `q` | query | string | no | "" |
| `limit` | query | integer | no | `50` |

| Status | Body |
|---|---|
| 200 | array of [DocumentTypeResponse](#documenttyperesponse) |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `POST /api/v1/document-types`

Create creates one document type from the JSON body.

Request body (required) (`application/json`): [CreateDocumentTypeRequest](#createdocumenttyperequest)

| Status | Body |
|---|---|
| 201 | [DocumentTypeResponse](#documenttyperesponse) |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `PUT /api/v1/document-types/{id}`

Update renames one document type.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

Request body (required) (`application/json`): [UpdateDocumentTypeRequest](#updatedocumenttyperequest)

| Status | Body |
|---|---|
| 200 | [DocumentTypeResponse](#documenttyperesponse) |
| 400 | object |
| 404 | string |
| 409 | object |
| 500 | string |

#### `DELETE /api/v1/document-types/{id}`

Delete removes one document type.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 204 | no body |
| 400 | object |
| 404 | string |
| 409 | object |
| 500 | string |

### Ingestion

#### `POST /api/v1/consume`

Consume scans the consumption directory and enqueues one batch: one consume task per new file with its waiting enrich (and thumbnail) children, deduplicating by MD5 content.

Unprocessable when the required tools are missing; an empty inbox reports no batch.

| Status | Body |
|---|---|
| 200 | object |
| 202 | object |
| 400 | object |
| 404 | no body |
| 409 | object |
| 422 | object |
| 500 | object |

#### `POST /api/v1/consume/upload`

Upload writes an uploaded file into the consumption directory and returns its served name.

Request body (`multipart/form-data`): object

| Status | Body |
|---|---|
| 202 | object |
| 400 | string |
| 413 | object |
| 422 | object |
| 500 | object |

### Tasks

#### `GET /api/v1/dashboard`

GetDashboard serves the dashboard: recent batches, running tasks, analytics, processing health and storage usage.

| Status | Body |
|---|---|
| 200 | [DashboardResponse](#dashboardresponse) |

#### `GET /api/v1/tasks`

ListTasks serves a batch's tasks, or with no batch filter the global task list.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `batch` | query | string | no | "" |
| `status` | query | string | no | "" |
| `limit` | query | integer | no | `20` |
| `offset` | query | integer | no | `0` |

| Status | Body |
|---|---|
| 200 | [ListTasksResponse](#listtasksresponse) |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `GET /api/v1/tasks/{id}`

GetTask serves one task's row with its progress.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 200 | [TaskResponse](#taskresponse) |
| 404 | string |
| 500 | string |

### Batches

#### `GET /api/v1/batches`

ListBatches serves one page of batch overviews, optionally filtered by batch status and by the presence of at least one task in a given task status; an invalid task_status is a client error.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `status` | query | string | no | "" |
| `task_status` | query | string | no | "" |
| `limit` | query | integer | no | `20` |
| `offset` | query | integer | no | `0` |

| Status | Body |
|---|---|
| 200 | [ListBatchesResponse](#listbatchesresponse) |
| 400 | string |
| 500 | string |

#### `GET /api/v1/batches/{id}`

GetBatchSummary serves one batch's aggregate row.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 200 | [BatchItem](#batchitem) |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `POST /api/v1/batches/{id}/cancel`

CancelBatch cancels a batch through the single cancel operation: pending, waiting and processing tasks cancel, a live executor is signalled, and the lease and locks are released.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 200 | object |
| 500 | string |
| default | object |

#### `POST /api/v1/batches/{id}/resume`

ResumeBatch requeues a settled or paused batch, optionally retrying its failed tasks; the retry intent is the retry_failed query parameter.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |
| `retry_failed` | query | boolean | no | — |

| Status | Body |
|---|---|
| 200 | object |
| 202 | object |
| 500 | string |
| 503 | string |
| default | object |

### Trash

#### `GET /api/v1/trash`

ListTrash serves the paged trash listing.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `limit` | query | integer | no | `50` |
| `offset` | query | integer | no | `0` |

| Status | Body |
|---|---|
| 200 | [TrashListResponse](#trashlistresponse) |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `POST /api/v1/trash/batch-delete`

BatchPermanentlyDelete permanently deletes many trashed documents, reporting per-document failures.

Request body (required) (`application/json`): [BatchDeleteRequest](#batchdeleterequest)

| Status | Body |
|---|---|
| 400 | object |
| default | [BatchDeleteResult](#batchdeleteresult) |

#### `POST /api/v1/trash/batch-restore`

BatchRestore restores many trashed documents, reporting per-document failures.

Request body (required) (`application/json`): [BatchDeleteRequest](#batchdeleterequest)

| Status | Body |
|---|---|
| 400 | object |
| default | [TrashRestoreResponse](#trashrestoreresponse) |

#### `POST /api/v1/trash/purge`

PurgeExpired purges the trash rows past their retention.

| Status | Body |
|---|---|
| 200 | object |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `GET /api/v1/trash/{id}`

GetTrashDocument serves one trashed document.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 200 | object |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `DELETE /api/v1/trash/{id}`

PermanentlyDeleteDocument removes one trashed document for good.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 204 | no body |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `POST /api/v1/trash/{id}/restore`

RestoreDocument moves one document back out of the trash.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 204 | no body |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

### Orphaned & errored

#### `GET /api/v1/errored`

ListErrored serves the errors-directory listing.

| Status | Body |
|---|---|
| 200 | array of object |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `DELETE /api/v1/errored`

DeleteErrored removes one errored file.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `subdir` | query | string | no | — |
| `file` | query | string | no | — |

| Status | Body |
|---|---|
| 204 | no body |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `POST /api/v1/errored/delete-all`

DeleteAllErrored clears the whole errors directory.

| Status | Body |
|---|---|
| 200 | object |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `GET /api/v1/errored/download`

DownloadErrored streams one errored original for inspection.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `subdir` | query | string | no | — |
| `file` | query | string | no | — |

| Status | Body |
|---|---|
| 200 | string (binary) |
| 400 | object |
| 404 | string |
| 409 | object |
| 500 | string |

#### `GET /api/v1/orphaned`

ListOrphaned serves the quarantined files.

| Status | Body |
|---|---|
| 200 | array of object |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `POST /api/v1/orphaned/delete-all`

DeleteAllOrphaned removes every quarantined file.

| Status | Body |
|---|---|
| 200 | object |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `POST /api/v1/orphaned/move-to-inbox-all`

MoveAllToInbox moves every quarantined file back into the consumption directory.

| Status | Body |
|---|---|
| 200 | object |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `POST /api/v1/orphaned/scan`

ScanOrphaned runs one scan-and-quarantine sweep.

| Status | Body |
|---|---|
| 200 | object |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `DELETE /api/v1/orphaned/{id}`

DeleteOrphaned removes one quarantined file.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 204 | no body |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `POST /api/v1/orphaned/{id}/move-to-inbox`

MoveToInbox copies one quarantined file back into the consumption directory.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 202 | no body |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `POST /api/v1/orphaned/{id}/restore`

RestoreOrphaned re-consumes one quarantined file.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 202 | no body |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

### Saved searches

#### `GET /api/v1/saved-searches`

List serves the saved searches of the requesting user.

| Status | Body |
|---|---|
| 200 | array of [SavedSearchResponse](#savedsearchresponse) |
| 500 | string |

#### `POST /api/v1/saved-searches`

Create stores one named structured filter.

Request body (required) (`application/json`): [CreateSavedSearchRequest](#createsavedsearchrequest)

| Status | Body |
|---|---|
| 201 | object |
| 400 | string |
| 500 | string |

#### `DELETE /api/v1/saved-searches/{id}`

Delete removes one saved search.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 204 | no body |
| 400 | string |
| 500 | string |

### LLM catalogs

#### `GET /api/v1/llm/catalog`

GetCatalog serves the full editable catalog: every provider and model with all their fields.

| Status | Body |
|---|---|
| 200 | [Catalog](#catalog) |
| 503 | object |

#### `GET /api/v1/llm/models`

ListModels serves the catalog's adapters, providers and models.

| Status | Body |
|---|---|
| 200 | [LlmModelsResponse](#llmmodelsresponse) |
| 503 | object |

#### `POST /api/v1/llm/models`

AddModel creates one model from the submitted fields.

Request body (required) (`application/json`): [ModelInput](#modelinput)

| Status | Body |
|---|---|
| 200 | [Catalog](#catalog) |
| 400 | string |
| 503 | object |

#### `PUT /api/v1/llm/models/{provider}/{model}`

SetModel rewrites a model's display name, limits and capability flags; its key is immutable.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `provider` | path | string | yes | — |
| `model` | path | string | yes | — |

Request body (required) (`application/json`): [ModelInput](#modelinput)

| Status | Body |
|---|---|
| 200 | [Catalog](#catalog) |
| 400 | string |
| 503 | object |

#### `DELETE /api/v1/llm/models/{provider}/{model}`

RemoveModel drops a model that no enricher configuration references.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `provider` | path | string | yes | — |
| `model` | path | string | yes | — |

| Status | Body |
|---|---|
| 200 | [Catalog](#catalog) |
| 503 | object |

#### `POST /api/v1/llm/providers`

AddProvider creates one provider from the submitted fields.

Request body (required) (`application/json`): [ProviderInput](#providerinput)

| Status | Body |
|---|---|
| 200 | [Catalog](#catalog) |
| 400 | string |
| 503 | object |

#### `PUT /api/v1/llm/providers/{name}`

SetProvider rewrites a provider's adapter, URL and session header; the name is its immutable key.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `name` | path | string | yes | — |

Request body (required) (`application/json`): [ProviderInput](#providerinput)

| Status | Body |
|---|---|
| 200 | [Catalog](#catalog) |
| 400 | string |
| 503 | object |

#### `DELETE /api/v1/llm/providers/{name}`

RemoveProvider drops a provider; the cascade query parameter also deletes its models.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `cascade` | query | string | no | — |
| `name` | path | string | yes | — |

| Status | Body |
|---|---|
| 200 | [Catalog](#catalog) |
| 400 | string |
| 503 | object |

#### `POST /api/v1/llm/reset`

ResetCatalogs restores the embedded defaults for the catalogs named in the request body.

It is the recovery path when the registry failed to load: it rewrites the files, loads a fresh registry and installs it, so every catalog endpoint serves again without a restart.

Request body (required) (`application/json`): object

| Status | Body |
|---|---|
| 200 | [Catalog](#catalog) |
| 400 | string |
| default | object |

### Users

#### `GET /api/v1/users`

List serves the paged user listing.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `limit` | query | integer | no | `50` |
| `offset` | query | integer | no | `0` |

| Status | Body |
|---|---|
| 200 | [UserListResponse](#userlistresponse) |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `POST /api/v1/users`

Create creates one user from the JSON body.

Request body (required) (`application/json`): [CreateUserRequest](#createuserrequest)

| Status | Body |
|---|---|
| 201 | [UserResponse](#userresponse) |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `GET /api/v1/users/{id}`

Get serves one user without the hash.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 200 | [UserResponse](#userresponse) |
| 400 | object |
| 404 | string |
| 409 | object |
| 500 | string |

#### `PUT /api/v1/users/{id}`

Update changes one user's username, password and role.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

Request body (required) (`application/json`): [UpdateUserRequest](#updateuserrequest)

| Status | Body |
|---|---|
| 200 | [UserResponse](#userresponse) |
| 400 | object |
| 404 | no body |
| 409 | object |
| 500 | string |

#### `DELETE /api/v1/users/{id}`

Delete removes one user.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 204 | no body |
| 400 | object |
| 404 | string |
| 409 | object |
| 500 | string |

#### `GET /api/v1/users/{id}/api-key`

GetKeyStatus reports a user's key status without secrets.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 200 | [APIKeyStatusResponse](#apikeystatusresponse) |
| 400 | object |
| 403 | string |
| 404 | string |
| 409 | object |
| 500 | string |

#### `POST /api/v1/users/{id}/api-key`

GenerateKey mints an API key for the path's user id.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 201 | [CreateAPIKeyResponse](#createapikeyresponse) |
| 400 | object |
| 403 | string |
| 404 | string |
| 409 | object |
| 500 | string |

#### `PUT /api/v1/users/{id}/api-key`

RotateKey replaces a user's API key atomically.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 200 | [CreateAPIKeyResponse](#createapikeyresponse) |
| 400 | object |
| 403 | string |
| 404 | string |
| 409 | object |
| 500 | string |

#### `DELETE /api/v1/users/{id}/api-key`

RevokeKey clears a user's API key.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `id` | path | string | yes | — |

| Status | Body |
|---|---|
| 204 | no body |
| 400 | object |
| 403 | string |
| 404 | string |
| 409 | object |
| 500 | string |

### Session

#### `POST /api/v1/auth/login`

Login verifies the credentials and sets the session cookie.

Request body (required) (`application/json`): [loginRequest](#loginrequest)

| Status | Body |
|---|---|
| 200 | [loginResponse](#loginresponse) |
| 400 | string |
| 401 | object |
| 500 | string |

#### `POST /api/v1/auth/logout`

Logout clears the session cookie.

| Status | Body |
|---|---|
| 204 | no body |

### Me

#### `GET /api/v1/me`

MeHandler serves the authenticated user's identity.

| Status | Body |
|---|---|
| 200 | [UserResponse](#userresponse) |
| 400 | object |
| 401 | string |
| 404 | string |
| 409 | object |
| 500 | string |

#### `GET /api/v1/me/api-key`

MeGetKeyStatus reports the calling user's key status.

| Status | Body |
|---|---|
| 200 | [APIKeyStatusResponse](#apikeystatusresponse) |
| 400 | object |
| 401 | string |
| 404 | string |
| 409 | object |
| 500 | string |

#### `POST /api/v1/me/api-key`

MeGenerateKey mints the calling user's own API key.

| Status | Body |
|---|---|
| 201 | [CreateAPIKeyResponse](#createapikeyresponse) |
| 400 | object |
| 401 | string |
| 404 | string |
| 409 | object |
| 500 | string |

#### `PUT /api/v1/me/api-key`

MeRotateKey rotates the calling user's API key.

| Status | Body |
|---|---|
| 200 | [CreateAPIKeyResponse](#createapikeyresponse) |
| 400 | object |
| 401 | string |
| 404 | string |
| 409 | object |
| 500 | string |

#### `DELETE /api/v1/me/api-key`

MeRevokeKey revokes the calling user's API key.

| Status | Body |
|---|---|
| 204 | no body |
| 400 | object |
| 401 | string |
| 404 | string |
| 409 | object |
| 500 | string |

### Logs

#### `GET /api/v1/logs/{name}`

ListLogs tails one process log, clamped between 100 and 5000 lines.

| Parameter | In | Type | Required | Default |
|---|---|---|---|---|
| `name` | path | string | yes | — |
| `lines` | query | string | no | — |

| Status | Body |
|---|---|
| 200 | object |
| 404 | object |
| 500 | object |

### Wizard & settings

#### `GET /wizard/bootstrap`

Bootstrap is the only /wizard/* route left reachable without authentication.

It exists because the SPA needs to know whether auth is enabled (and which tools are missing) before it can decide whether to show the login screen — so it must expose only non-sensitive fields. Anything else (LLM tokens, DB connection details, full status) belongs behind GetConfig/ConfigStatus. Bootstrap runs the environment bootstrap and switches the wizard into its post-bootstrap state.

| Status | Body |
|---|---|
| 200 | [BootstrapResponse](#bootstrapresponse) |

#### `GET /wizard/config`

GetConfig serves the sanitized configuration view.

| Status | Body |
|---|---|
| 200 | [ConfigResponse](#configresponse) |

#### `PUT /wizard/config`

PutConfig validates and saves the submitted configuration.

Request body (required) (`application/json`): object

| Status | Body |
|---|---|
| 200 | [DatabaseConfig](#databaseconfig) |
| 201 | object |
| 202 | object |
| 400 | string |
| 409 | object |
| 500 | object |
| 503 | string |

#### `POST /wizard/config/retry`

RetryFailedConfig retries the environment's failed work: it resumes the batches of failed migration tasks and re-triggers asset downloads for missing or failed assets.

| Status | Body |
|---|---|
| 200 | object |
| 400 | string |
| 500 | string |
| 503 | string |

#### `GET /wizard/config/status`

ConfigStatus serves the environment status the setup wizard polls: readiness, tool failures, pending or failed migration tasks and the direct asset downloads.

Until the database is bound—setup has not reached the schema step—nothing counts as configured or pending: asset downloads only start with the settings save, so the wizard keeps the operator on the settings step instead of polling work that was never started.

| Status | Body |
|---|---|
| 200 | [ConfigStatusResponse](#configstatusresponse) |

### Meta

#### `GET /api/v1/openapi.yaml`

ServeOpenAPISpec writes the embedded OpenAPI document to the response as YAML, under the same path the document itself lists.

The bytes are generated, not authored: never edit them by hand.

| Status | Body |
|---|---|
| 200 | string (binary) |

#### `GET /health`

| Status | Body |
|---|---|
| 200 | [HealthResponse](#healthresponse) |
| 500 | string |

## Schemas

### APIKeyStatusResponse

APIKeyStatusResponse says whether a user has an API key and its non-secret metadata.

| Field | Type | Required |
|---|---|---|
| `api_key_created_at` | string | no |
| `api_key_prefix` | string | no |
| `has_api_key` | boolean | yes |

### AddDocumentPeopleRequest

AddDocumentPeopleRequest attaches one person to a document with a role type.

| Field | Type | Required |
|---|---|---|
| `people_id` | integer | yes |
| `people_type_id` | integer | yes |

### AddDocumentTagRequest

AddDocumentTagRequest adds one tag by id.

| Field | Type | Required |
|---|---|---|
| `tag_id` | integer | yes |

### AppConfigResponse

AppConfigResponse is the app section's API view.

| Field | Type | Required |
|---|---|---|
| `initialized` | boolean | yes |
| `log_level` | string | yes |
| `logging` | [LoggingConfigResponse](#loggingconfigresponse) | yes |

### AvailableFileType

AvailableFileType is one ingest format the UI can offer.

| Field | Type | Required |
|---|---|---|
| `extensions` | array of string (nullable) | yes |
| `label` | string | yes |
| `mime_type` | string | yes |
| `required` | boolean | yes |

### BackupConfigResponse

BackupConfigResponse is the backup section's API view.

| Field | Type | Required |
|---|---|---|
| `enabled` | boolean | yes |
| `path` | string | yes |
| `schedules` | array of [BackupScheduleResponse](#backupscheduleresponse) (nullable) | yes |

### BackupScheduleResponse

BackupScheduleResponse is one backup schedule's API view.

| Field | Type | Required |
|---|---|---|
| `interval` | number | yes |
| `keep` | integer | yes |
| `mode` | string | yes |
| `path` | string | yes |
| `time` | string | yes |

### BatchDeleteError

BatchDeleteError is one document that could not be deleted.

| Field | Type | Required |
|---|---|---|
| `error` | string | yes |
| `id` | string | yes |

### BatchDeleteRequest

BatchDeleteRequest is the bulk soft-delete request.

| Field | Type | Required |
|---|---|---|
| `document_ids` | array of string (nullable) | yes |

### BatchDeleteResult

BatchDeleteResult reports the bulk soft delete: how many went through and the per-document failures.

| Field | Type | Required |
|---|---|---|
| `deleted` | integer | yes |
| `failed` | array of [BatchDeleteError](#batchdeleteerror) | no |

### BatchDocumentTypeError

BatchDocumentTypeError is one document whose type change failed.

| Field | Type | Required |
|---|---|---|
| `error` | string | yes |
| `id` | string | yes |

### BatchDocumentTypeRequest

BatchDocumentTypeRequest sets one document type over many documents.

| Field | Type | Required |
|---|---|---|
| `document_ids` | array of string (nullable) | yes |
| `document_type_id` | integer | yes |

### BatchDocumentTypeResult

BatchDocumentTypeResult reports the bulk type change outcome.

| Field | Type | Required |
|---|---|---|
| `failed` | array of [BatchDocumentTypeError](#batchdocumenttypeerror) | no |
| `updated` | integer | yes |

### BatchItem

BatchItem is one batch as every surface shows it: identity, status, origin, schedule, the per-status counts, the owner's liveness and the run duration once terminal.

| Field | Type | Required |
|---|---|---|
| `batch_id` | string | yes |
| `cancelled` | integer | yes |
| `completed` | integer | yes |
| `created_at` | string | no |
| `discarded` | integer | yes |
| `duration_ms` | integer | no |
| `failed` | integer | yes |
| `origin` | string | yes |
| `orphaned` | boolean | yes |
| `owner_pid` | integer | no |
| `owner_state` | string | no |
| `pending` | integer | yes |
| `processing` | integer | yes |
| `schedule` | string | no |
| `status` | string | yes |
| `total` | integer | yes |
| `waiting` | integer | yes |

### BatchTagError

BatchTagError is one document whose tag change failed.

| Field | Type | Required |
|---|---|---|
| `error` | string | yes |
| `id` | string | yes |

### BatchTagRequest

BatchTagRequest is a bulk tag change: documents, tags, and the mode (add or remove).

| Field | Type | Required |
|---|---|---|
| `document_ids` | array of string (nullable) | yes |
| `mode` | string | yes |
| `tag_ids` | array of integer (nullable) | yes |

### BatchTagResult

BatchTagResult reports the bulk tag change outcome.

| Field | Type | Required |
|---|---|---|
| `assigned` | integer | yes |
| `failed` | array of [BatchTagError](#batchtagerror) | no |

### BootstrapResponse

BootstrapResponse reports the bootstrap outcome: whether the server now authenticates and which tools are still missing.

| Field | Type | Required |
|---|---|---|
| `auth_enabled` | boolean | yes |
| `missing_tools` | array of [ExternalTool](#externaltool) (nullable) | yes |

### Catalog

Catalog is the full editable catalog: every provider, including the ones with no models, and every model entry, both ordered by name.

| Field | Type | Required |
|---|---|---|
| `models` | array of [ModelInput](#modelinput) (nullable) | yes |
| `providers` | array of [ProviderInput](#providerinput) (nullable) | yes |

### CompanionTool

CompanionTool is a binary an engine wraps (ocrmypdf needs tesseract and unpaper): whether it is present and whether its absence blocks the engine.

| Field | Type | Required |
|---|---|---|
| `available` | boolean | yes |
| `command` | string | yes |
| `install_hints` | object (nullable) | yes |
| `purpose` | string | yes |
| `required` | boolean | yes |

### ConfigResponse

ConfigResponse is the full configuration as the API shows it: every section view plus the engines and file types the UI offers.

| Field | Type | Required |
|---|---|---|
| `app` | [AppConfigResponse](#appconfigresponse) | yes |
| `available_engines` | object (nullable) | yes |
| `available_file_types` | array of [AvailableFileType](#availablefiletype) (nullable) | yes |
| `backup` | [BackupConfigResponse](#backupconfigresponse) | yes |
| `consumer` | [ConsumerConfigResponse](#consumerconfigresponse) | yes |
| `database` | [DatabaseConfigResponse](#databaseconfigresponse) | yes |
| `enricher` | [EnricherConfigResponse](#enricherconfigresponse) | yes |
| `mirror` | [MirrorConfigResponse](#mirrorconfigresponse) | yes |
| `server` | [ServerConfigResponse](#serverconfigresponse) | yes |
| `storage` | [StorageConfigResponse](#storageconfigresponse) | yes |

### ConfigStatusResponse

ConfigStatusResponse is the environment status the wizard polls.

| Field | Type | Required |
|---|---|---|
| `configured` | boolean | yes |
| `errors` | array of string (nullable) | yes |
| `failed_tasks` | array of [FailedTaskSummary](#failedtasksummary) | no |
| `missing_tools` | array of [ExternalTool](#externaltool) (nullable) | yes |
| `pending_tasks` | integer | yes |
| `tools` | array of [ExternalTool](#externaltool) (nullable) | yes |

### ConsumerConfigResponse

ConsumerConfigResponse is the consumer section's API view.

| Field | Type | Required |
|---|---|---|
| `converter` | [DocxOdtConverterResponse](#docxodtconverterresponse) | yes |
| `max_files_per_batch` | integer | yes |
| `ocr` | [OCRResponse](#ocrresponse) | yes |
| `pdfoptimizer` | [PdfOptimizerResponse](#pdfoptimizerresponse) | yes |
| `polling` | [PollingConfigResponse](#pollingconfigresponse) | yes |
| `reclaim` | [ReclaimConfigResponse](#reclaimconfigresponse) | yes |
| `supported_files` | array of string (nullable) | yes |
| `textextractor` | [TextExtractorResponse](#textextractorresponse) | yes |
| `thumbnail` | [ThumbnailConfigResponse](#thumbnailconfigresponse) | yes |
| `workers` | integer | yes |

### ContentAnalyzerResponse

ContentAnalyzerResponse is the analyzer section's API view.

| Field | Type | Required |
|---|---|---|
| `doc_type_refinement` | [DocTypeRefinementResponse](#doctyperefinementresponse) | yes |
| `enabled` | boolean | yes |
| `fallbacks` | array of [FallbackConfigResponse](#fallbackconfigresponse) | no |
| `llm` | [LlmConfigResponse](#llmconfigresponse) | yes |
| `min_tags` | integer | yes |
| `pause_on_credit_error` | boolean | yes |
| `prompt_template` | string | no |
| `timeout` | integer | yes |

### CoverageFlagActionRequest

CoverageFlagActionRequest is the body of the coverage flag apply and dismiss actions.

| Field | Type | Required |
|---|---|---|
| `document_id` | integer | yes |
| `tag_name` | string | yes |

### CoverageFlagListResponse

CoverageFlagListResponse is one page of open coverage flags.

| Field | Type | Required |
|---|---|---|
| `results` | array of [CoverageFlagResponse](#coverageflagresponse) (nullable) | yes |
| `total` | integer | yes |

### CoverageFlagResponse

CoverageFlagResponse is one open coverage flag as the API shows it.

| Field | Type | Required |
|---|---|---|
| `class` | string | yes |
| `created_at` | string (date-time) | yes |
| `document_id` | integer | yes |
| `document_title` | string | yes |
| `id` | integer | yes |
| `provisional` | boolean | yes |
| `source` | string | yes |
| `tag_name` | string | yes |

### CreateAPIKeyResponse

CreateAPIKeyResponse returns the freshly minted API key, which the client must store because only its hash is kept server-side.

| Field | Type | Required |
|---|---|---|
| `api_key` | string | yes |
| `message` | string | yes |
| `prefix` | string | yes |

### CreateAuditValueRequest

CreateAuditValueRequest is the body of the coverage create-value action: it names the missing dimension to register and carries no document, since the value is a vocabulary addition.

| Field | Type | Required |
|---|---|---|
| `tag_name` | string | yes |

### CreateDocumentTypeRequest

CreateDocumentTypeRequest is the body of a document type create.

| Field | Type | Required |
|---|---|---|
| `description` | string | yes |
| `name` | string | yes |

### CreatePeopleTypeRequest

CreatePeopleTypeRequest is the body of a people type create.

| Field | Type | Required |
|---|---|---|
| `description` | string | yes |
| `name` | string | yes |

### CreatePersonRequest

CreatePersonRequest is the body of a person create: romanized name and the optional native-script name.

| Field | Type | Required |
|---|---|---|
| `name` | string | yes |
| `name_native` | string | yes |

### CreateSavedSearchRequest

CreateSavedSearchRequest is the body of a saved-search create: a name and the structured filter as raw JSON.

| Field | Type | Required |
|---|---|---|
| `filter` | any JSON | yes |
| `name` | string | yes |

### CreateTagRequest

CreateTagRequest is the body of POST /api/v1/tags.

| Field | Type | Required |
|---|---|---|
| `name` | string | yes |

### CreateUserRequest

CreateUserRequest is the body of POST /api/v1/users; an empty role defaults to viewer.

| Field | Type | Required |
|---|---|---|
| `password` | string | yes |
| `role` | string | no |
| `username` | string | yes |

### DashboardResponse

DashboardResponse is everything the dashboard needs in one payload.

| Field | Type | Required |
|---|---|---|
| `analytics` | [DocumentAnalytics](#documentanalytics) | no |
| `avg_file_size_bytes` | integer | yes |
| `cancelled` | integer | yes |
| `completed` | integer | yes |
| `discarded` | integer | yes |
| `failed` | integer | yes |
| `inbox_files` | integer | yes |
| `original_type_breakdown` | array of [OriginalTypeStat](#originaltypestat) (nullable) | yes |
| `originals_size_bytes` | integer | yes |
| `pending` | integer | yes |
| `processed_size_bytes` | integer | yes |
| `processing` | integer | yes |
| `processing_health` | [ProcessingHealth](#processinghealth) | no |
| `recent_batches` | array of [BatchItem](#batchitem) (nullable) | yes |
| `running_tasks` | [RunningTasksResponse](#runningtasksresponse) | yes |
| `storage_trend` | array of [StorageTrendPoint](#storagetrendpoint) (nullable) | yes |
| `total_batches` | integer | yes |
| `total_files` | integer | yes |
| `total_pages` | integer | yes |
| `total_words` | integer | yes |
| `waiting` | integer | yes |

### DatabaseConfig

DatabaseConfig is the PostgreSQL section: a discrete connection or a full DSN, the container runtime used by the database-backed tests, and the idempotent seeders to apply.

| Field | Type | Required |
|---|---|---|
| `container` | string | yes |
| `database` | string | yes |
| `dsn` | string | yes |
| `host` | string | yes |
| `password` | string | yes |
| `port` | integer | yes |
| `runtime` | string | yes |
| `seeders` | array of string (nullable) | yes |
| `sslmode` | string | yes |
| `user` | string | yes |

### DatabaseConfigResponse

DatabaseConfigResponse is the database section's API view, without the password.

| Field | Type | Required |
|---|---|---|
| `container` | string | yes |
| `database` | string | yes |
| `host` | string | yes |
| `port` | integer | yes |
| `runtime` | string | yes |
| `sslmode` | string | yes |
| `user` | string | yes |

### DateRange

DateRange is one date bound pair, each side optional.

| Field | Type | Required |
|---|---|---|
| `from` | string (nullable) | yes |
| `to` | string (nullable) | yes |

### DeletedTagListResponse

DeletedTagListResponse is one page of deleted-tag records.

| Field | Type | Required |
|---|---|---|
| `results` | array of [DeletedTagResponse](#deletedtagresponse) (nullable) | yes |
| `total` | integer | yes |

### DeletedTagResponse

DeletedTagResponse is one deleted-tag record as the API shows it: the name, its kind (an operator block), and when.

| Field | Type | Required |
|---|---|---|
| `deleted_at` | string (date-time) | yes |
| `kind` | string | yes |
| `name` | string | yes |

### DistributionItem

DistributionItem is one labeled bucket of a distribution chart.

| Field | Type | Required |
|---|---|---|
| `count` | integer | yes |
| `label` | string | yes |

### DocTypeRefinementResponse

DocTypeRefinementResponse is the type-refinement section's API view.

| Field | Type | Required |
|---|---|---|
| `enabled` | boolean | yes |
| `head_words` | integer | yes |
| `tail_words` | integer | yes |

### DocumentAnalytics

DocumentAnalytics is the dashboard's document analytics: language, type and tag distributions plus the missing-state counts.

| Field | Type | Required |
|---|---|---|
| `document_type_distribution` | array of [DistributionItem](#distributionitem) (nullable) | yes |
| `language_distribution` | array of [DistributionItem](#distributionitem) (nullable) | yes |
| `missing_language_count` | integer | yes |
| `missing_tags_count` | integer | yes |
| `missing_type_count` | integer | yes |
| `tag_frequency` | array of [DistributionItem](#distributionitem) (nullable) | yes |

### DocumentDownloadRequest

DocumentDownloadRequest lists the documents a download should cover.

| Field | Type | Required |
|---|---|---|
| `document_ids` | array of string (nullable) | yes |

### DocumentResponse

DocumentResponse is the stored document as the API shows it: identity, counts, type, attached tags and people.

| Field | Type | Required |
|---|---|---|
| `char_count` | integer | yes |
| `created_at` | string | yes |
| `document_type_id` | integer | no |
| `document_type_name` | string | no |
| `file_size` | integer | yes |
| `has_thumbnail` | boolean | yes |
| `id` | string | yes |
| `language` | string | yes |
| `md5_checksum` | string | yes |
| `modified_at` | string | yes |
| `original_type` | string | yes |
| `page_count` | integer | yes |
| `people` | array of [PersonResponse](#personresponse) | no |
| `sha512_checksum` | string | yes |
| `tags` | array of [TagResponse](#tagresponse) | no |
| `title` | string | yes |
| `word_count` | integer | yes |

### DocumentTypeResponse

DocumentTypeResponse is a document type as the API shows it, with the documents carrying it.

| Field | Type | Required |
|---|---|---|
| `description` | string | yes |
| `document_count` | integer | yes |
| `id` | integer | yes |
| `name` | string | yes |

### DocumentUpdateRequest

DocumentUpdateRequest is the body of a document update: title, type, language, and optionally the corrected text.

| Field | Type | Required |
|---|---|---|
| `document_type_id` | integer | yes |
| `language` | string | yes |
| `text_content` | string | no |
| `title` | string | yes |

### DocxOdtConverterResponse

DocxOdtConverterResponse is the converter section's API view.

| Field | Type | Required |
|---|---|---|
| `binary` | string | yes |
| `enabled` | boolean | yes |
| `timeout` | integer | yes |

### EngineEntry

EngineEntry is one selectable engine for the setup UI: its value and display label.

| Field | Type | Required |
|---|---|---|
| `label` | string | yes |
| `value` | string | yes |

### EnricherConfigResponse

EnricherConfigResponse is the enricher section's API view.

| Field | Type | Required |
|---|---|---|
| `contentanalyzer` | [ContentAnalyzerResponse](#contentanalyzerresponse) | yes |
| `tag_adjudicator` | [TagAdjudicatorConfigResponse](#tagadjudicatorconfigresponse) | yes |
| `tag_audit` | [TagAuditConfigResponse](#tagauditconfigresponse) | yes |
| `tag_maintenance` | [TagMaintenanceConfigResponse](#tagmaintenanceconfigresponse) | yes |
| `tag_measure` | [TagMeasureConfigResponse](#tagmeasureconfigresponse) | yes |
| `tag_quality` | [TagQualityConfigResponse](#tagqualityconfigresponse) | yes |
| `tag_reconcile` | [TagReconcileConfigResponse](#tagreconcileconfigresponse) | yes |
| `tagmatcher` | [TagMatcherResponse](#tagmatcherresponse) | yes |
| `textreducer` | [TextReducerResponse](#textreducerresponse) | yes |
| `workers` | integer | yes |

### ExternalTool

ExternalTool is one external capability's readiness report for the UI: the engine, its category, the command looked up, whether it is on PATH, and the install hints per platform. OCR engines carry their configured languages with per-language hints and their companion binaries.

| Field | Type | Required |
|---|---|---|
| `available` | boolean | yes |
| `category` | string | yes |
| `command` | string | yes |
| `companions` | array of [CompanionTool](#companiontool) | no |
| `engine` | string | yes |
| `install_hints` | object (nullable) | yes |
| `lang_hints` | array of [LangHint](#langhint) | no |
| `languages` | array of string | no |
| `purpose` | string | no |

### FTSDocumentResponse

FTSDocumentResponse is a full-text search hit: the document view plus the tsvector rank, snippet and the stored text.

| Field | Type | Required |
|---|---|---|
| `char_count` | integer | yes |
| `created_at` | string | yes |
| `document_type_id` | integer | no |
| `document_type_name` | string | no |
| `file_size` | integer | yes |
| `has_thumbnail` | boolean | yes |
| `id` | string | yes |
| `language` | string | yes |
| `md5_checksum` | string | yes |
| `modified_at` | string | yes |
| `page_count` | integer | yes |
| `people` | array of [PersonResponse](#personresponse) | no |
| `rank` | number | yes |
| `sha512_checksum` | string | yes |
| `snippet` | string | yes |
| `tags` | array of [TagResponse](#tagresponse) | no |
| `text_content` | string | yes |
| `title` | string | yes |
| `word_count` | integer | yes |

### FailedTaskSummary

FailedTaskSummary is one failed environment unit: a migration task, or a direct asset download that carries no task id.

| Field | Type | Required |
|---|---|---|
| `error` | string | yes |
| `lang` | string | no |
| `op` | string | yes |
| `task_id` | string | yes |

### FallbackConfigResponse

FallbackConfigResponse is one fallback's API view.

| Field | Type | Required |
|---|---|---|
| `enabled` | boolean | yes |
| `llm` | [LlmConfigResponse](#llmconfigresponse) | yes |

### FileSizeRange

FileSizeRange is one size bound pair in bytes, each side optional.

| Field | Type | Required |
|---|---|---|
| `max` | integer (nullable) | yes |
| `min` | integer (nullable) | yes |

### Filter

Filter is the structured search request: the optional free-text query, tag and people filters, document type and language, date and size bounds, sorting and paging, and the missing-state flags (documents with no language, no type, or no tags at all).

| Field | Type | Required |
|---|---|---|
| `date_created` | [DateRange](#daterange) (nullable) | yes |
| `date_modified` | [DateRange](#daterange) (nullable) | yes |
| `document_type` | string | yes |
| `file_size` | [FileSizeRange](#filesizerange) (nullable) | yes |
| `language` | string | yes |
| `limit` | integer | yes |
| `missing_language` | boolean | yes |
| `missing_type` | boolean | yes |
| `offset` | integer | yes |
| `people` | array of [PersonFilter](#personfilter) (nullable) | yes |
| `query` | string | yes |
| `sort_by` | string | yes |
| `sort_order` | string | yes |
| `tags` | array of string (nullable) | yes |
| `untagged` | boolean | yes |

### HealthResponse

HealthResponse is the liveness probe payload.

| Field | Type | Required |
|---|---|---|
| `status` | string | yes |
| `time` | string | yes |
| `version` | string | yes |

### HugotResponse

HugotResponse is the embedding-model section's API view.

| Field | Type | Required |
|---|---|---|
| `backend` | string | yes |
| `model` | string | yes |

### LangHint

LangHint is one OCR language's install hints per platform.

| Field | Type | Required |
|---|---|---|
| `install_hints` | object (nullable) | yes |
| `language` | string | yes |

### ListBatchesResponse

ListBatchesResponse wraps the batch items.

| Field | Type | Required |
|---|---|---|
| `batches` | array of [BatchItem](#batchitem) (nullable) | yes |

### ListTasksResponse

ListTasksResponse is one batch's task list, optionally with its overview.

| Field | Type | Required |
|---|---|---|
| `batch_id` | string | no |
| `summary` | [BatchItem](#batchitem) | no |
| `tasks` | array of [TaskResponse](#taskresponse) (nullable) | yes |

### LlmConfigResponse

LlmConfigResponse is one LLM endpoint's API view, without the token.

| Field | Type | Required |
|---|---|---|
| `adapter` | string | yes |
| `model` | string | yes |
| `provider` | string | yes |
| `reasoning` | boolean | yes |
| `request_delay` | number | yes |
| `temperature` | number | yes |
| `token` | string | no |

### LlmModelEntry

LlmModelEntry is one model in the UI's model catalog view.

| Field | Type | Required |
|---|---|---|
| `capabilities` | object | yes |
| `id` | string | yes |

### LlmModelsResponse

LlmModelsResponse is the model catalog as the config UI shows it.

| Field | Type | Required |
|---|---|---|
| `adapters` | object (nullable) | yes |
| `providers` | object (nullable) | yes |

### LoggingConfigResponse

LoggingConfigResponse is the API view of the log rotation.

| Field | Type | Required |
|---|---|---|
| `compress` | boolean | yes |
| `max_age` | integer | yes |
| `max_backups` | integer | yes |
| `max_size` | integer | yes |

### MimeInfo

MimeInfo describes one supported format: its canonical MIME type, the canonical file extension, and the short label shown in the UI.

| Field | Type | Required |
|---|---|---|
| `extension` | string | yes |
| `label` | string | yes |
| `mime_type` | string | yes |

### MirrorConfigResponse

MirrorConfigResponse is the mirror section's API view.

| Field | Type | Required |
|---|---|---|
| `enabled` | boolean | yes |
| `interval` | number | yes |
| `path` | string | yes |
| `time` | string | yes |

### ModelInput

ModelInput is a model's editable catalog fields, mirroring the on-disk entry: its key (provider and model id), display name, token limits and capability flags.

| Field | Type | Required |
|---|---|---|
| `disable_reasoning` | boolean | no |
| `display_name` | string | no |
| `max_input_tokens` | integer | no |
| `max_output_tokens` | integer | no |
| `model_id` | string | yes |
| `provider` | string | yes |
| `reasoning_efforts` | array of string | no |
| `supports_prompt_caching` | boolean | no |
| `supports_structured_output` | boolean | no |
| `supports_temperature` | boolean | no |
| `uses_max_completion_tokens` | boolean | no |
| `uses_responses_endpoint` | boolean | no |

### OCRResponse

OCRResponse is the OCR section's API view.

| Field | Type | Required |
|---|---|---|
| `data_dir` | string | yes |
| `engine` | string | yes |
| `languages` | array of string (nullable) | yes |
| `ocr_workers` | integer | yes |
| `timeout` | integer | yes |

### OriginalTypeStat

OriginalTypeStat is one file-type bucket of the storage usage.

| Field | Type | Required |
|---|---|---|
| `count` | integer | yes |
| `original_type` | string | yes |
| `total_bytes` | integer | yes |

### PdfOptimizerResponse

PdfOptimizerResponse is the optimizer section's API view.

| Field | Type | Required |
|---|---|---|
| `engine` | string | yes |
| `fallback` | string | yes |
| `timeout` | integer | yes |

### PeopleTypeResponse

PeopleTypeResponse is a people type as the API shows it.

| Field | Type | Required |
|---|---|---|
| `description` | string | yes |
| `id` | integer | yes |
| `name` | string | yes |

### PersonFilter

PersonFilter is one person clause: a name and, optionally, the person type.

| Field | Type | Required |
|---|---|---|
| `name` | string | yes |
| `type` | string | yes |

### PersonListResponse

PersonListResponse is one page of people.

| Field | Type | Required |
|---|---|---|
| `results` | array of [PersonResponse](#personresponse) (nullable) | yes |
| `total` | integer | yes |

### PersonResponse

PersonResponse is a person as attached to a document, including the type's display fields.

| Field | Type | Required |
|---|---|---|
| `document_count` | integer | yes |
| `id` | integer | yes |
| `name` | string | yes |
| `name_native` | string | no |
| `person_type_description` | string | yes |
| `person_type_id` | integer | yes |
| `person_type_name` | string | yes |

### PollingConfigResponse

PollingConfigResponse is the polling section's API view.

| Field | Type | Required |
|---|---|---|
| `enabled` | boolean | yes |
| `interval` | integer | yes |
| `windows` | array of [PollingWindowResponse](#pollingwindowresponse) (nullable) | yes |

### PollingWindowResponse

PollingWindowResponse is one polling window.

| Field | Type | Required |
|---|---|---|
| `end` | string | yes |
| `start` | string | yes |

### ProcessingHealth

ProcessingHealth is the dashboard's processing health: success rate, last-week counts, average duration, and the batch and tool health.

| Field | Type | Required |
|---|---|---|
| `active_batches` | integer | yes |
| `avg_duration_ms` | integer | yes |
| `completed_last_7d` | integer | yes |
| `failed_last_7d` | integer | yes |
| `missing_tools` | integer | yes |
| `orphaned_batches` | integer | yes |
| `success_rate` | number | yes |

### ProviderInput

ProviderInput is a provider's editable catalog fields: the name (its key), the adapter kind, the endpoint base URL, and the optional session header the provider expects its client session identifier in.

| Field | Type | Required |
|---|---|---|
| `adapter` | string | yes |
| `official_url` | string | yes |
| `provider` | string | yes |
| `session_header` | string | no |

### ProvisionalTagListResponse

ProvisionalTagListResponse is one page of provisional tags: the filtered total for the queue's pager and the queue-level pending identity count that decides the reevaluation affordance.

| Field | Type | Required |
|---|---|---|
| `pending_identity_total` | integer | yes |
| `results` | array of [ProvisionalTagResponse](#provisionaltagresponse) (nullable) | yes |
| `total` | integer | yes |

### ProvisionalTagResponse

ProvisionalTagResponse describes a tag awaiting review, with its latest verdict and what keeps it provisional.

| Field | Type | Required |
|---|---|---|
| `composition_hold` | string | no |
| `composition_reason` | string | no |
| `created_at` | string (date-time) | yes |
| `demoted_at` | string (date-time) | no |
| `demoted_reason` | string | no |
| `document_count` | integer | yes |
| `id` | integer | yes |
| `identity_hold` | string | no |
| `name` | string | yes |
| `pending_identity` | integer | yes |
| `verdict` | [TagQualityVerdictInfo](#tagqualityverdictinfo) (nullable) | yes |

### ReclaimConfigResponse

ReclaimConfigResponse is the reclaim section's API view.

| Field | Type | Required |
|---|---|---|
| `enabled` | boolean | yes |
| `max_retries` | integer | yes |
| `stale_task_after` | integer | yes |

### RemoveDocumentPeopleRequest

RemoveDocumentPeopleRequest removes one person-role assignment.

| Field | Type | Required |
|---|---|---|
| `people_id` | integer | yes |
| `people_type_id` | integer | yes |

### RemoveDocumentTagRequest

RemoveDocumentTagRequest removes one tag by id.

| Field | Type | Required |
|---|---|---|
| `tag_id` | integer | yes |

### RunningTasksResponse

RunningTasksResponse lists the currently processing tasks.

| Field | Type | Required |
|---|---|---|
| `count` | integer | yes |
| `tasks` | array of [TaskResponse](#taskresponse) (nullable) | yes |

### SavedSearchResponse

SavedSearchResponse is one saved search as the API shows it.

| Field | Type | Required |
|---|---|---|
| `created_at` | string (date-time) | yes |
| `filter` | any JSON | yes |
| `id` | integer | yes |
| `name` | string | yes |

### SearchResponse

SearchResponse is one page of search hits.

| Field | Type | Required |
|---|---|---|
| `results` | array of [FTSDocumentResponse](#ftsdocumentresponse) (nullable) | yes |
| `total` | integer | yes |

### ServerConfigResponse

ServerConfigResponse is the server section's API view.

| Field | Type | Required |
|---|---|---|
| `auth_enabled` | boolean | yes |
| `host` | string | yes |
| `max_batch_delete` | integer | yes |
| `max_concurrent_batches` | integer | yes |
| `max_download_files` | integer | yes |
| `max_download_size_mb` | integer | yes |
| `max_upload_size` | integer | yes |
| `port` | integer | yes |

### SiblingListResponse

SiblingListResponse is the latest measurement run's containment readings with the run identity they came from; Absent marks the zero-data state where no measurement run exists yet.

| Field | Type | Required |
|---|---|---|
| `absent` | boolean | yes |
| `results` | array of [SiblingReadingResponse](#siblingreadingresponse) (nullable) | yes |
| `run_id` | string | no |
| `snapshot_at` | string (date-time) | no |
| `total` | integer | yes |

### SiblingReadingResponse

SiblingReadingResponse is one near-contained tag pair, A holding nearly all of B's documents: containment is the fraction of B's live documents that A also holds, and the masses are those live document counts.

| Field | Type | Required |
|---|---|---|
| `a` | string | yes |
| `b` | string | yes |
| `containment` | number | yes |
| `gray` | boolean | yes |
| `mass_a` | integer | yes |
| `mass_b` | integer | yes |

### StorageConfigResponse

StorageConfigResponse is the storage section's API view.

| Field | Type | Required |
|---|---|---|
| `consumption_dir` | string | yes |
| `migration_mode` | string | yes |
| `storage_dir` | string | yes |
| `trash` | [TrashConfigResponse](#trashconfigresponse) | yes |

### StorageTrendPoint

StorageTrendPoint is one day of the storage usage trend: documents and bytes that day, plus the running total.

| Field | Type | Required |
|---|---|---|
| `cumulative_bytes` | integer | yes |
| `daily_bytes` | integer | yes |
| `daily_count` | integer | yes |
| `date` | string | yes |

### TagAdjudicatorConfigResponse

TagAdjudicatorConfigResponse is the adjudicator section's API view.

| Field | Type | Required |
|---|---|---|
| `enabled` | boolean | yes |
| `llm` | [TagAdjudicatorLlmResponse](#tagadjudicatorllmresponse) | yes |
| `timeout` | integer | yes |

### TagAdjudicatorLlmResponse

TagAdjudicatorLlmResponse is one LLM endpoint of the judged steps.

| Field | Type | Required |
|---|---|---|
| `adapter` | string | yes |
| `model` | string | yes |
| `provider` | string | yes |
| `request_delay` | number | yes |
| `token` | string | no |

### TagAuditConfigResponse

TagAuditConfigResponse is the coverage audit section's API view.

| Field | Type | Required |
|---|---|---|
| `enabled` | boolean | yes |
| `interval` | number | yes |
| `sample_size` | integer | yes |
| `time` | string | yes |

### TagListResponse

TagListResponse is one page of the tag listing.

| Field | Type | Required |
|---|---|---|
| `results` | array of [TagResponse](#tagresponse) (nullable) | yes |
| `total` | integer | yes |

### TagMaintenanceConfigResponse

TagMaintenanceConfigResponse is the maintenance routine section's API view.

| Field | Type | Required |
|---|---|---|
| `enabled` | boolean | yes |
| `interval` | number | yes |
| `time` | string | yes |

### TagMatcherResponse

TagMatcherResponse is the matcher section's API view.

| Field | Type | Required |
|---|---|---|
| `chunk_size` | integer | yes |
| `hugot` | [HugotResponse](#hugotresponse) | yes |
| `reduce_target_words` | integer | yes |
| `timeout` | integer | yes |
| `top_n` | integer | yes |

### TagMeasureConfigResponse

TagMeasureConfigResponse is the measure section's API view.

| Field | Type | Required |
|---|---|---|
| `after_enrichment_batch` | boolean | yes |
| `enabled` | boolean | yes |
| `interval` | number | yes |
| `time` | string | yes |

### TagQualityConfigResponse

TagQualityConfigResponse is the quality judge's API view.

| Field | Type | Required |
|---|---|---|
| `enabled` | boolean | yes |
| `llm` | [TagAdjudicatorLlmResponse](#tagadjudicatorllmresponse) | yes |
| `timeout` | integer | yes |

### TagQualityCriteriaView

TagQualityCriteriaView is the API view of the judge's rubric answers; a nil answer had no signal in the stored payload.

| Field | Type | Required |
|---|---|---|
| `atomic` | boolean (nullable) | yes |
| `defer` | boolean (nullable) | yes |
| `dimension` | boolean (nullable) | yes |
| `not_form_genre` | boolean (nullable) | yes |
| `not_person` | boolean (nullable) | yes |
| `subject_matter` | boolean (nullable) | yes |

### TagQualityVerdictInfo

TagQualityVerdictInfo is the latest recorded quality verdict for a provisional tag: the pass flag, the recorded outcome (absent on rows predating the outcome column), its criteria (nil when unjudged) and the model.

| Field | Type | Required |
|---|---|---|
| `criteria` | [TagQualityCriteriaView](#tagqualitycriteriaview) (nullable) | yes |
| `model` | string | no |
| `outcome` | string | no |
| `pass` | boolean | yes |
| `verdict_at` | string (date-time) | yes |

### TagReconcileConfigResponse

TagReconcileConfigResponse is the reconcile section's API view.

| Field | Type | Required |
|---|---|---|
| `dead_tag_grace_days` | integer | yes |
| `enabled` | boolean | yes |
| `interval` | number | yes |
| `max_rejudge` | integer | yes |
| `max_retag` | integer | yes |
| `provisional_grace_days` | integer | yes |
| `report_only` | boolean | yes |
| `time` | string | yes |

### TagReconcileReportResponse

TagReconcileReportResponse points at the task whose result holds the reconcile report.

| Field | Type | Required |
|---|---|---|
| `completed_at` | string | yes |
| `result` | any JSON | yes |
| `task_id` | string | yes |

### TagResponse

TagResponse carries a tag on the vocabulary and document surfaces: the vocabulary listing fills both DocumentCount — every assignment, trashed documents included — and Promoted, while Seeded marks the operator-owned inventory the demote action must leave alone.

| Field | Type | Required |
|---|---|---|
| `document_count` | integer | yes |
| `id` | integer | yes |
| `name` | string | yes |
| `promoted` | boolean | yes |
| `seeded` | boolean | yes |

### TaskProgressResponse

TaskProgressResponse is the persisted progress snapshot of a task.

| Field | Type | Required |
|---|---|---|
| `detail` | string | no |
| `pct` | number | no |
| `step` | string | yes |
| `updated_at` | string | yes |

### TaskResponse

TaskResponse is one task as the API shows it: identity, status, the payload's file name and document id, the label, the progress snapshot, and the result once done.

| Field | Type | Required |
|---|---|---|
| `batch_id` | string | yes |
| `completed_at` | string (nullable) | yes |
| `created_at` | string | yes |
| `error` | string (nullable) | yes |
| `file_name` | string | yes |
| `label` | string | yes |
| `payload_doc_id` | string | yes |
| `progress` | [TaskProgressResponse](#taskprogressresponse) | no |
| `result` | any JSON | no |
| `started_at` | string (nullable) | yes |
| `status` | string | yes |
| `task_id` | string | yes |
| `task_type` | string | yes |

### TextExtractorResponse

TextExtractorResponse is the extractor section's API view.

| Field | Type | Required |
|---|---|---|
| `engine` | string | yes |
| `timeout` | integer | yes |

### TextReducerResponse

TextReducerResponse is the reducer section's API view.

| Field | Type | Required |
|---|---|---|
| `engine` | string | yes |
| `target_words` | integer | yes |
| `timeout` | integer | yes |

### ThumbnailConfigResponse

ThumbnailConfigResponse is the thumbnail section's API view.

| Field | Type | Required |
|---|---|---|
| `backfill_interval` | number | yes |
| `backfill_time` | string | yes |
| `dpi` | integer | yes |
| `enabled` | boolean | yes |
| `engine` | string | yes |
| `max_width` | integer | yes |
| `quality` | integer | yes |
| `timeout` | integer | yes |
| `workers` | integer | yes |

### TrashConfigResponse

TrashConfigResponse is the trash section's API view.

| Field | Type | Required |
|---|---|---|
| `retention_days` | integer | yes |

### TrashDocumentResponse

TrashDocumentResponse is one trashed document as the API shows it.

| Field | Type | Required |
|---|---|---|
| `created_at` | string (date-time) | yes |
| `deleted_at` | string (date-time) | yes |
| `document_id` | string | yes |
| `file_size` | integer | yes |
| `id` | integer | yes |
| `language` | string | yes |
| `original_type` | string | yes |
| `page_count` | integer | yes |
| `title` | string | yes |

### TrashListResponse

TrashListResponse is one page of the trash listing.

| Field | Type | Required |
|---|---|---|
| `documents` | array of [TrashDocumentResponse](#trashdocumentresponse) (nullable) | yes |
| `limit` | integer | yes |
| `offset` | integer | yes |
| `total` | integer | yes |

### TrashRestoreResponse

TrashRestoreResponse reports a bulk trash restore.

| Field | Type | Required |
|---|---|---|
| `failed` | array of [BatchDeleteError](#batchdeleteerror) | no |
| `restored` | integer | yes |

### UpdateDocumentTypeRequest

UpdateDocumentTypeRequest is the body of a document type update.

| Field | Type | Required |
|---|---|---|
| `description` | string | yes |
| `name` | string | yes |

### UpdatePeopleTypeRequest

UpdatePeopleTypeRequest is the body of a people type update.

| Field | Type | Required |
|---|---|---|
| `description` | string | yes |
| `name` | string | yes |

### UpdatePersonRequest

UpdatePersonRequest is the body of a person update.

| Field | Type | Required |
|---|---|---|
| `name` | string | yes |
| `name_native` | string | yes |

### UpdateTagRequest

UpdateTagRequest is the body of a tag rename.

| Field | Type | Required |
|---|---|---|
| `name` | string | yes |

### UpdateUserRequest

UpdateUserRequest is the body of a user update; an empty password keeps the current one.

| Field | Type | Required |
|---|---|---|
| `password` | string | no |
| `role` | string | no |
| `username` | string | yes |

### UserListResponse

UserListResponse is one page of users.

| Field | Type | Required |
|---|---|---|
| `total` | integer | yes |
| `users` | array of [UserResponse](#userresponse) (nullable) | yes |

### UserResponse

UserResponse is a user as the API shows it: no hash, but the API key's existence, prefix and creation time.

| Field | Type | Required |
|---|---|---|
| `api_key_created_at` | string | no |
| `api_key_prefix` | string | no |
| `created_at` | string | yes |
| `has_api_key` | boolean | yes |
| `id` | integer | yes |
| `role` | string | yes |
| `username` | string | yes |

### loginRequest

| Field | Type | Required |
|---|---|---|
| `password` | string | yes |
| `username` | string | yes |

### loginResponse

| Field | Type | Required |
|---|---|---|
| `token` | string | yes |
| `user` | [UserResponse](#userresponse) | yes |

