# OpenAI-compatible model adapter + local filesystem storage

This fork adds a pragmatic local-development path for running ADK with non-Gemini models and without external storage middleware.

## 1. OpenAI-compatible model adapter

Package:

```go
import adkopenai "google.golang.org/adk/model/openai"
```

Create a model directly:

```go
llm, err := adkopenai.NewModel(
    "gpt-4.1-mini",
    adkopenai.WithBaseURL("https://api.openai.com/v1"),
    adkopenai.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
)
```

For OpenAI-compatible providers:

```go
llm, err := adkopenai.NewModel(
    "deepseek-chat",
    adkopenai.WithBaseURL("https://api.deepseek.com/v1"),
    adkopenai.WithAPIKey(os.Getenv("DEEPSEEK_API_KEY")),
)
```

Environment fallback:

```bash
export OPENAI_COMPAT_BASE_URL="https://api.openai.com/v1"
export OPENAI_COMPAT_API_KEY="sk-..."
```

`internal/configurable` now treats Gemini model names as Gemini and all other model names as OpenAI-compatible. A YAML model value like this will use the OpenAI-compatible adapter:

```yaml
model: openai/gpt-4.1-mini
```

The `openai/` prefix is stripped before sending the request. You can also set:

```yaml
model: deepseek-chat
```

and configure the endpoint through `OPENAI_COMPAT_BASE_URL`.

## 2. Tool calling behavior

ADK `genai.FunctionDeclaration` values are translated to OpenAI Chat Completions tools:

```json
{
  "type": "function",
  "function": {
    "name": "tool_name",
    "description": "...",
    "parameters": { "type": "object", "properties": {} }
  }
}
```

Model tool calls are translated back into `genai.Part{FunctionCall: ...}` so the existing ADK tool execution loop continues to work.

Tool results are sent as OpenAI `role: "tool"` messages with:

- `tool_call_id`
- `content` as a JSON string

This is the critical compatibility requirement that prevents second-round tool-calling errors.

Strict tool schema mode is disabled by default because many current ADK-generated schemas do not mark every property required and may be rejected by OpenAI strict mode. You can enable it explicitly:

```go
adkopenai.WithStrictTools(true)
```

## 3. Structured JSON output

When ADK sets:

```go
GenerateContentConfig.ResponseMIMEType = "application/json"
GenerateContentConfig.ResponseSchema = schema
```

this adapter sends Chat Completions `response_format: { type: "json_schema", ... }`.

Strict response mode is currently set to `false` to preserve compatibility with existing schemas. Tighten this later when the schema generator is normalized.

## 4. Local filesystem storage

Three new local storage constructors were added.

### Session

```go
sessionSvc, err := session.FileSystemService("./data/sessions")
```

This implementation wraps the existing in-memory service and persists a JSON snapshot after every mutation:

```text
./data/sessions/sessions.snapshot.json
```

Use this for local development and single-process deployment. Use the database service for production multi-instance deployment.

### Artifact

```go
artifactSvc, err := artifact.FileSystemService("./data/artifacts")
```

Path layout:

```text
./data/artifacts/<app>/<user>/<session-or-user>/<file>/<version>.json
```

Path segments are base64url encoded. Logical artifact names are not used as raw filesystem paths.

### Memory

```go
memorySvc, err := memory.FileSystemService("./data/memory")
```

Path layout:

```text
./data/memory/<app>/<user>/<session>.json
```

This mirrors the existing in-memory keyword search behavior and is intended as a simple local backend before replacing memory with pgvector, Qdrant, Milvus, Elasticsearch, or another retrieval service.

## 5. Recommended local bootstrap

```go
sessionSvc, err := session.FileSystemService("./data/sessions")
if err != nil { panic(err) }

artifactSvc, err := artifact.FileSystemService("./data/artifacts")
if err != nil { panic(err) }

memorySvc, err := memory.FileSystemService("./data/memory")
if err != nil { panic(err) }

llm, err := adkopenai.NewModel("gpt-4.1-mini")
if err != nil { panic(err) }
```

Then pass these into your existing `runner.Runner` construction exactly like the current in-memory services.

## 6. Follow-up hardening checklist

- Add a neutral model schema so core no longer depends on `google.golang.org/genai` request/response types.
- Add a storage factory driven by `config.yaml`.
- Split filesystem session persistence from one snapshot into append-only event files if you need replay and partial recovery.
- Add signed URL support for artifact downloads.
- Add pgvector/qdrant-backed memory.
- Add OpenAI Responses API adapter after Chat Completions compatibility is stable.
