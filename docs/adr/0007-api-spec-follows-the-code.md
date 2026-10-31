# 0007. The API spec follows the code

**Status:** accepted

## Context

The REST surface changes with every feature, and clients, the embedded spec, and the
generated reference all depend on a specification that matches what the server actually
serves. Maintaining an OpenAPI document or endpoint inventory by hand creates two sources
of truth: every handler change needs a parallel spec edit, drift accumulates silently,
and it is only discovered when a client or a consumer breaks.

## Decision

The code is the only source of truth. `apispec` statically analyzes the HTTP surface and
writes the committed `internal/api/spec/openapi.yaml` (`make api-spec`); edub serves the
embedded spec at `GET /api/v1/openapi.yaml`; `make api-docs` renders `docs/api.md` from
it; CI's `api-spec-check` fails when the committed artifacts lag the code. Intent static
analysis cannot infer — parameter encodings, the roles matrix — is expressed in
`apispec.yaml` overrides and the hand-written intro of `docs/api.md`, never by annotating
handlers or editing the generated files.

## Consequences

API review is code review: a surface change without regenerated artifacts fails CI, and
the spec cannot describe anything the code does not express. The cost is that some
documentation has designated hand-written homes outside the generated files.

Standing constraint: never hand-edit `internal/api/spec/openapi.yaml` or the generated
`docs/api.md`; an API surface change lands in the same change as its regenerated
artifacts.
