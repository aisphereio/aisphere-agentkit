# Asset State Management Implementation Notes

## 2026-06-04 MinIO Artifact Backend

First landing step:

- `artifact.Service` now has a MinIO/S3 adapter.
- `storage.artifact.type` accepts `minio` and `s3`.
- Generated artifacts can move out of the local filesystem into object storage by configuring endpoint, bucket, credentials, prefix, and path-style access.

Current boundary:

- Upload storage is still filesystem-backed in this patch.
- Project artifact registry is still an artifact JSON document, so it can still create many registry versions.
- Idempotent processing state still needs the PostgreSQL `assets`, `asset_versions`, and `processing_jobs` layer described in `asset-state-management-design.md`.

Next implementation steps:

1. Add the database-backed asset catalog and use MinIO object keys as version payload pointers.
2. Move project artifact registry reads from "load one growing JSON artifact" to database queries.
3. Add processor fingerprints for `normalize_utf8`, `split_book_preview`, `split_book_commit`, and `extract_skill_batch`.
4. Make long tasks resume from the latest completed checkpoint instead of re-scanning existing artifacts.
