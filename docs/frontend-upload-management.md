# Frontend Upload Management Direction

The frontend must treat uploads as workspace resources, not as prompt text.

## Target UX

1. User selects or drops a file.
2. Frontend uploads it to `POST /api/platform/uploads`.
3. Backend returns an upload record with `upload_id`, `handling_mode`, and policy flags.
4. Frontend renders a file card in the workspace/upload panel.
5. The chat message contains only a reference, for example:

```text
我上传了 upload_id=up_xxx，文件名=demo.txt，purpose=book_source，请先检查并处理。
```

The full file content must not be placed in `newMessage.parts`.

## UI modules

Recommended layout:

```text
Left/side panel: Uploads
  - list of upload cards
  - filters: purpose, app, session, handling mode
  - actions: preview, attach artifact, delete

Main panel: Agent chat
  - user can insert an upload reference into the message
  - user can choose "send to current agent"

Detail panel/modal:
  - metadata
  - bounded preview
  - suggested next actions
```

## Upload card fields

Each card should show:

```text
original_name
size_bytes
mime_type
upload_id
purpose
handling_mode
inline_eligible
previewable
created_at
policy_reason
```

## File handling rules

| upload type | frontend behavior |
|---|---|
| small text with `inline_small_text` | may offer "insert preview/text" after explicit user action |
| book/source text with `preprocess_required` | offer "send to book_dissector" or "attach artifact" |
| PDF/DOCX | offer extract/index processors when implemented |
| XLSX/CSV | offer table profile/preview processors |
| ZIP/code package | offer unpack/scan processors |
| image | show preview; do not inline unless visual analysis is requested |
| blocked | show warning and delete option |

## Current API calls

### Upload

```ts
const form = new FormData();
form.append("file", file);
form.append("purpose", "book_source");
form.append("app_name", currentAppName);
form.append("session_id", currentSessionId);

const upload = await fetch("/api/platform/uploads", {
  method: "POST",
  body: form,
}).then(r => r.json());
```

### List

```ts
const uploads = await fetch(
  `/api/platform/uploads?app_name=${appName}&session_id=${sessionId}`,
).then(r => r.json());
```

### Preview

```ts
const preview = await fetch(
  `/api/platform/uploads/${uploadId}/preview?max_bytes=8192`,
).then(r => r.json());
```

### Attach to artifact

```ts
await fetch(`/api/platform/uploads/${uploadId}/attach-artifact`, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({
    app_name: appName,
    session_id: sessionId,
    artifact_name: upload.original_name,
  }),
});
```

## Chat message rule

When a user clicks "send to agent", build a small text message:

```ts
const text = `我选择了平台上传文件：upload_id=${upload.id}，文件名=${upload.original_name}，handling_mode=${upload.handling_mode}，purpose=${upload.purpose}。请不要读取整文件进上下文，先调用 UploadToolset 检查并按需预处理。`;
```

Then send the normal `/run_sse` request with that text only.

## Backend guard

Even if the frontend regresses, the runtime input policy rejects large inline text/blob parts with HTTP 413. This is intentional: large files belong in Upload Center, not in the LLM prompt.

## Discover upload config

Frontend can discover backend upload policy:

```ts
const uploadConfig = await fetch("/api/uploads/config").then(r => r.json());
```

The response contains endpoints, inline guards, and available handling modes. Use this to avoid hardcoding thresholds in the frontend.
