# Platform Uploads

Platform Uploads is the raw-file intake layer for user-provided files.
It is intentionally separated from Artifact management.

## Why this exists

Large user files, such as TXT books, should not be placed into `newMessage.parts` and sent directly to the model. The correct flow is:

```text
user file -> platform upload storage -> upload metadata/id -> explicit preprocessing/tool step -> optional artifact attachment -> agent analysis
```

This prevents a whole book or large PDF from being injected into the LLM context before a deterministic tool has a chance to split or index it.

## Storage

Upload metadata is stored in the platform database table managed by `internal/platform/uploads`.
Upload bytes are stored under:

```yaml
storage:
  upload:
    type: filesystem
    root: ./.adk/data/uploads
```

## API

### Upload a file

```http
POST /api/platform/uploads
Content-Type: multipart/form-data
```

Multipart fields:

- `file`: required file field
- `project_id`: optional
- `app_name`: optional
- `session_id`: optional
- `purpose`: optional, for example `book_source`
- `metadata_json`: optional JSON string

Example PowerShell:

```powershell
$form = @{
  file = Get-Item "E:\books\demo.txt"
  purpose = "book_source"
  app_name = "book_dissector"
  session_id = "book001"
}
Invoke-RestMethod -Method Post -Uri "http://localhost:8080/api/platform/uploads" -Form $form
```

### List uploads

```http
GET /api/platform/uploads?purpose=book_source&app_name=book_dissector&session_id=book001
```

### Get metadata

```http
GET /api/platform/uploads/{upload_id}
```

### Download raw content

```http
GET /api/platform/uploads/{upload_id}/content
```

### Attach an upload to an artifact workspace

```http
POST /api/platform/uploads/{upload_id}/attach-artifact
Content-Type: application/json

{
  "app_name": "book_dissector",
  "session_id": "book001",
  "artifact_name": "demo.txt"
}
```

After attaching, an agent can call artifact-based tools such as `book_split_from_artifact` using `source_artifact=demo.txt` without the raw TXT appearing in model context.

## Handling policy

Every upload is classified when it is created. The policy is returned in list/get responses:

```json
{
  "handling_mode": "preprocess_required",
  "inline_eligible": false,
  "previewable": true,
  "policy_reason": "book sources must be split/indexed before agent analysis"
}
```

Supported `handling_mode` values:

| mode | meaning |
|---|---|
| `reference_only` | Store as a file reference. Do not inline by default. |
| `inline_small_text` | Small text that may be explicitly inlined after user/tool confirmation. |
| `preprocess_required` | Must be split, extracted, chunked, or indexed before model use. |
| `retrieval_index` | Intended for retrieval/chunk index workflows. |
| `tool_workspace` | Intended for deterministic scripts/tools such as XLSX profiling or ZIP unpacking. |
| `artifact_ready` | Already promoted/converted to an artifact. |
| `blocked` | Unsafe or unsupported file type. |

Default principle:

```text
Upload file bytes never enter model context automatically.
The model receives upload_id, metadata, bounded previews, or tool-produced chunks/artifacts.
```

## Preview a bounded slice

```http
GET /api/platform/uploads/{upload_id}/preview?max_bytes=8192
```

The preview endpoint returns at most a small, configured slice of a text-like file. It is for UI inspection and routing decisions, not for full-document analysis.

Example response:

```json
{
  "upload_id": "...",
  "original_name": "demo.txt",
  "mime_type": "text/plain; charset=utf-8",
  "size_bytes": 1234567,
  "handling_mode": "preprocess_required",
  "previewable": true,
  "display_mode": "text",
  "encoding": "utf-8",
  "content": "第一章 ...",
  "bytes_read": 8192,
  "truncated": true,
  "warning": "preview is bounded and must not be treated as full file content"
}
```

## Runtime input guard

The runtime now has an input policy that rejects large inline message content before it reaches the model:

```yaml
runtime:
  input_policy:
    reject_large_inline: true
    max_inline_text_chars: 64000
    max_inline_data_bytes: 262144
```

If a frontend accidentally places a large TXT/PDF/base64 payload in `newMessage.parts`, `/run` and `/run_sse` return `413` with a message explaining that the file must go through `/platform/uploads` first.

This is the backend safety net. The frontend should still prevent the mistake by design.

## Agent-facing UploadToolset

Agents can be given `UploadToolset` to inspect platform uploads without loading raw content into the model.

Available tools:

- `upload_list`: list upload metadata for the current app/user/session.
- `upload_get`: get one upload by `upload_id`.
- `upload_preview`: return a bounded preview for text-like files.
- `upload_attach_artifact`: attach an upload to the current artifact workspace.

Example `root_agent.yaml`:

```yaml
tools:
  - name: UploadToolset
  - name: BookPreprocessorToolset
  - name: list_artifacts
  - name: load_artifacts
  - name: save_artifact
```

Book dissection flow:

```text
upload_id -> upload_get -> upload_attach_artifact -> book_split_from_artifact -> book_get_chapter -> LLM chapter analysis
```

## Frontend contract

Do not read file content and put it into chat messages.

Wrong:

```text
FileReader.readAsText(file) -> newMessage.parts[0].text = entire file
```

Right:

```text
file -> POST /api/platform/uploads -> upload_id
chat message -> "I uploaded upload_id=...; please process it."
```

A file card should display:

- file name
- size
- MIME type
- `upload_id`
- `handling_mode`
- preview button
- attach/promote button
- delete button

## Developer scripts

Convenience scripts live in:

```text
scripts/uploads/
```

They are useful for testing Upload Center without the frontend.

## Frontend metadata endpoint

```http
GET /api/uploads/config
```

Returns upload endpoints and runtime inline limits for frontend integration.
