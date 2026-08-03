// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package openai implements the model.LLM interface by calling an
// OpenAI-compatible /v1/chat/completions endpoint.
//
// It is intentionally dependency-light: the adapter speaks plain HTTP so it can
// work with OpenAI, DeepSeek, Qwen-compatible gateways, local vLLM, LiteLLM,
// OneAPI, and other OpenAI-compatible providers.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/genai"

	// "google.golang.org/adk/internal/runtimeconfig"
	"google.golang.org/adk/internal/runtimetrace"
	"google.golang.org/adk/internal/version"
	"google.golang.org/adk/model"
)

const (
	defaultBaseURL = "https://api.openai.com/v1"
	chatPath       = "/chat/completions"
)

// Option customizes an OpenAI-compatible model.
type Option func(*Config)

// Config configures the OpenAI-compatible model adapter.
type Config struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
	Headers    http.Header

	// UserAgent is sent as the user-agent header. Empty means a Google ADK
	// compatible value is used.
	UserAgent string

	// StrictTools controls whether function tool schemas are sent with
	// strict=true. Default is false because many existing ADK/jsonschema-go
	// schemas do not mark every property required and may be rejected by strict
	// OpenAI tool validation.
	StrictTools bool

	// ExtraBody fields are merged into every chat completion request. This is the
	// escape hatch for provider-specific OpenAI-compatible params, for example:
	// reasoning_effort, enable_thinking, stream_options, response_format overrides.
	ExtraBody map[string]any
}

// WithBaseURL sets the OpenAI-compatible base URL, for example
// https://api.openai.com/v1 or https://api.deepseek.com/v1.
func WithBaseURL(baseURL string) Option { return func(c *Config) { c.BaseURL = baseURL } }

// WithAPIKey sets the bearer token.
func WithAPIKey(apiKey string) Option { return func(c *Config) { c.APIKey = apiKey } }

// WithHTTPClient sets the HTTP client.
func WithHTTPClient(client *http.Client) Option { return func(c *Config) { c.HTTPClient = client } }

// WithHeader adds a static header to every request.
func WithHeader(key, value string) Option {
	return func(c *Config) {
		if c.Headers == nil {
			c.Headers = make(http.Header)
		}
		c.Headers.Set(key, value)
	}
}

// WithHeaders replaces/merges static headers.
func WithHeaders(headers http.Header) Option {
	return func(c *Config) {
		if c.Headers == nil {
			c.Headers = make(http.Header)
		}
		for k, values := range headers {
			for _, v := range values {
				c.Headers.Add(k, v)
			}
		}
	}
}

// WithUserAgent sets the user-agent header.
func WithUserAgent(userAgent string) Option { return func(c *Config) { c.UserAgent = userAgent } }

// WithStrictTools enables or disables OpenAI strict function schema mode.
func WithStrictTools(strict bool) Option { return func(c *Config) { c.StrictTools = strict } }

// WithExtraBody merges provider-specific fields into every request body.
func WithExtraBody(extra map[string]any) Option {
	return func(c *Config) {
		c.ExtraBody = cloneMap(extra)
	}
}

// Model is an OpenAI-compatible implementation of model.LLM.
type Model struct {
	name       string
	baseURL    string
	apiKey     string
	httpClient *http.Client
	headers    http.Header
	userAgent  string
	strict     bool
	extraBody  map[string]any
}

var _ model.LLM = (*Model)(nil)

// NewModel returns an OpenAI-compatible model implementation.
//
// Environment fallbacks:
//   - OPENAI_COMPAT_BASE_URL or OPENAI_BASE_URL for BaseURL
//   - OPENAI_COMPAT_API_KEY or OPENAI_API_KEY for APIKey
func NewModel(modelName string, opts ...Option) (*Model, error) {
	cfg := &Config{
		BaseURL:    firstNonEmpty(os.Getenv("OPENAI_COMPAT_BASE_URL"), os.Getenv("OPENAI_BASE_URL"), defaultBaseURL),
		APIKey:     firstNonEmpty(os.Getenv("OPENAI_COMPAT_API_KEY"), os.Getenv("OPENAI_API_KEY")),
		HTTPClient: http.DefaultClient,
		Headers:    make(http.Header),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	if modelName == "" {
		return nil, errors.New("openai model name is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	baseURL, err := normalizeBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	ua := cfg.UserAgent
	if ua == "" {
		ua = "google-adk/" + version.Version
	}
	return &Model{
		name:       modelName,
		baseURL:    baseURL,
		apiKey:     cfg.APIKey,
		httpClient: cfg.HTTPClient,
		headers:    cloneHeader(cfg.Headers),
		userAgent:  ua,
		strict:     cfg.StrictTools,
		extraBody:  cloneMap(cfg.ExtraBody),
	}, nil
}

func (m *Model) Name() string { return m.name }

// GenerateContent calls the OpenAI-compatible chat completion endpoint.
func (m *Model) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	if req == nil {
		return single(nil, errors.New("nil LLM request"))
	}
	m.maybeAppendUserContent(req)
	body, err := m.buildRequest(req, stream)
	if err != nil {
		return single(nil, err)
	}
	runtimetrace.Record(ctx, "openai.request.payload", map[string]any{
		"base_url": m.baseURL,
		"path":     chatPath,
		"stream":   stream,
		"body":     body,
	})
	if stream {
		return m.generateStream(ctx, body)
	}
	return func(yield func(*model.LLMResponse, error) bool) {
		resp, err := m.generate(ctx, body)
		yield(resp, err)
	}
}

func single(resp *model.LLMResponse, err error) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) { yield(resp, err) }
}

func (m *Model) modelName(req *model.LLMRequest) string {
	if req != nil && req.Model != "" {
		return req.Model
	}
	return m.name
}

func (m *Model) buildRequest(req *model.LLMRequest, stream bool) (chatCompletionRequest, error) {
	out := chatCompletionRequest{
		Model:    m.modelName(req),
		Stream:   stream,
		Extra:    cloneMap(m.extraBody),
		Messages: make([]chatMessage, 0, len(req.Contents)+1),
	}
	if req.Config != nil {
		if req.Config.SystemInstruction != nil && len(req.Config.SystemInstruction.Parts) > 0 {
			out.Messages = append(out.Messages, chatMessage{Role: "system", Content: contentToOpenAI(req.Config.SystemInstruction, "system")})
		}
		out.Tools = toolsToOpenAI(req.Config.Tools, m.strict)
		if len(out.Tools) > 0 {
			out.ToolChoice = "auto"
		}
		if req.Config.Temperature != nil {
			out.Temperature = req.Config.Temperature
		}
		if req.Config.ResponseMIMEType == "application/json" && req.Config.ResponseSchema != nil {
			out.ResponseFormat = map[string]any{
				"type": "json_schema",
				"json_schema": map[string]any{
					"name":   "adk_response",
					"strict": false,
					"schema": req.Config.ResponseSchema,
				},
			}
		}
	}
	for _, content := range req.Contents {
		if content == nil {
			continue
		}
		msgs, err := contentToMessages(content)
		if err != nil {
			return out, err
		}
		out.Messages = append(out.Messages, msgs...)
	}
	return out, nil
}

func (m *Model) generate(ctx context.Context, body chatCompletionRequest) (*model.LLMResponse, error) {
	var resp chatCompletionResponse
	if err := m.doJSON(ctx, body, &resp); err != nil {
		runtimetrace.Record(ctx, "openai.response.error", map[string]any{"error": err.Error()})
		return nil, err
	}
	runtimetrace.Record(ctx, "openai.response.raw", map[string]any{"response": resp})
	return responseToLLM(&resp), nil
}

func (m *Model) generateStream(ctx context.Context, body chatCompletionRequest) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		body.Stream = true
		payload, err := json.Marshal(body)
		if err != nil {
			yield(nil, err)
			return
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+chatPath, bytes.NewReader(payload))
		if err != nil {
			yield(nil, err)
			return
		}
		m.decorate(httpReq, "text/event-stream")
		httpResp, err := m.httpClient.Do(httpReq)
		if err != nil {
			yield(nil, err)
			return
		}
		defer httpResp.Body.Close()
		if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
			yield(nil, decodeError(httpResp))
			return
		}

		acc := &streamAccumulator{}
		scanner := bufio.NewScanner(httpResp.Body)
		// Model deltas can be larger than the default 64K token limit.
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				break
			}
			var chunk chatCompletionChunk

			if runtimetrace.DumpStreamChunks(ctx) {
				runtimetrace.Record(ctx, "openai.stream.raw", map[string]any{
					"data": data,
				})
			}

			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				yield(nil, fmt.Errorf("decode stream chunk: %w", err))
				return
			}

			partials, final := acc.add(&chunk)

			if runtimetrace.DumpStreamChunks(ctx) {
				runtimetrace.Record(ctx, "openai.stream.chunk", map[string]any{
					"chunk": chunk,
				})
			}

			for _, p := range partials {
				if !yield(p, nil) {
					return
				}
			}
			if final != nil {
				runtimetrace.Record(ctx, "openai.stream.final", map[string]any{"response": final})
				if !yield(final, nil) {
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			yield(nil, err)
			return
		}
		if final := acc.final(); final != nil {
			yield(final, nil)
		}
	}
}

func (m *Model) doJSON(ctx context.Context, body chatCompletionRequest, target any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+chatPath, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	m.decorate(httpReq, "application/json")
	httpResp, err := m.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return decodeError(httpResp)
	}
	if err := json.NewDecoder(httpResp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode openai response: %w", err)
	}
	return nil
}

func (m *Model) decorate(req *http.Request, accept string) {
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", accept)
	if m.userAgent != "" {
		req.Header.Set("user-agent", m.userAgent)
	}
	if m.apiKey != "" {
		req.Header.Set("authorization", "Bearer "+m.apiKey)
	}
	for k, values := range m.headers {
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}
}

// maybeAppendUserContent mirrors the Gemini adapter behavior. Chat Completions
// strongly expects the conversation to end with a user/tool message before the
// assistant continues.
func (m *Model) maybeAppendUserContent(req *model.LLMRequest) {
	if len(req.Contents) == 0 {
		req.Contents = append(req.Contents, genai.NewContentFromText("Handle the requests as specified in the System Instruction.", "user"))
		return
	}
	last := req.Contents[len(req.Contents)-1]
	if last != nil && last.Role == "model" {
		req.Contents = append(req.Contents, genai.NewContentFromText("Continue processing previous requests as instructed. Exit or provide a summary if no more outputs are needed.", "user"))
	}
}

func contentToMessages(c *genai.Content) ([]chatMessage, error) {
	role := roleToOpenAI(c.Role)
	var messages []chatMessage
	var normalParts []*genai.Part
	var toolCalls []toolCall
	for _, p := range c.Parts {
		if p == nil {
			continue
		}
		if p.FunctionResponse != nil {
			content, err := json.Marshal(p.FunctionResponse.Response)
			if err != nil {
				return nil, fmt.Errorf("marshal function response %q: %w", p.FunctionResponse.Name, err)
			}
			id := p.FunctionResponse.ID
			if id == "" {
				id = p.FunctionResponse.Name
			}
			messages = append(messages, chatMessage{Role: "tool", ToolCallID: id, Content: string(content)})
			continue
		}
		if p.FunctionCall != nil {
			args := "{}"
			if p.FunctionCall.Args != nil {
				b, err := json.Marshal(p.FunctionCall.Args)
				if err != nil {
					return nil, fmt.Errorf("marshal function call %q args: %w", p.FunctionCall.Name, err)
				}
				args = string(b)
			}
			id := p.FunctionCall.ID
			if id == "" {
				id = p.FunctionCall.Name
			}
			toolCalls = append(toolCalls, toolCall{ID: id, Type: "function", Function: toolCallFunction{Name: p.FunctionCall.Name, Arguments: args}})
			continue
		}
		normalParts = append(normalParts, p)
	}
	if len(normalParts) > 0 || len(toolCalls) > 0 {
		msg := chatMessage{Role: role, Content: partsToOpenAI(normalParts, role), ToolCalls: toolCalls}
		// OpenAI tool_calls are assistant messages even if an upstream provider stored
		// the role differently.
		if len(toolCalls) > 0 {
			msg.Role = "assistant"
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

func contentToOpenAI(c *genai.Content, role string) any {
	if c == nil {
		return ""
	}
	return partsToOpenAI(c.Parts, role)
}

func partsToOpenAI(parts []*genai.Part, role string) any {
	var out []chatContentPart
	for _, p := range parts {
		if p == nil {
			continue
		}
		if p.Text != "" {
			out = append(out, chatContentPart{Type: "text", Text: p.Text})
		}
		if p.InlineData != nil && len(p.InlineData.Data) > 0 {
			mt := p.InlineData.MIMEType
			if strings.HasPrefix(mt, "image/") && role == "user" {
				out = append(out, chatContentPart{Type: "image_url", ImageURL: &imageURL{URL: dataURL(mt, p.InlineData.Data)}})
			} else {
				out = append(out, chatContentPart{Type: "text", Text: fmt.Sprintf("[inline file: mime=%s, base64=%s]", mt, base64.StdEncoding.EncodeToString(p.InlineData.Data))})
			}
		}
		if p.FileData != nil {
			out = append(out, chatContentPart{Type: "text", Text: fmt.Sprintf("[file: uri=%s, mime=%s]", p.FileData.FileURI, p.FileData.MIMEType)})
		}
		if p.FunctionCall != nil {
			// Function calls are represented by assistant.tool_calls, not content parts.
			continue
		}
	}
	if len(out) == 0 {
		return ""
	}
	// Chat Completions assistant/system content is most compatible as a string.
	// Keep user multimodal content as parts when non-text parts exist.
	if role != "user" || len(out) == 1 && out[0].Type == "text" {
		var b strings.Builder
		for _, part := range out {
			if part.Text != "" {
				b.WriteString(part.Text)
			}
		}
		return b.String()
	}
	return out
}

func toolsToOpenAI(tools []*genai.Tool, strict bool) []chatTool {
	var out []chatTool
	for _, t := range tools {
		if t == nil {
			continue
		}
		for _, decl := range t.FunctionDeclarations {
			if decl == nil || decl.Name == "" {
				continue
			}
			params := any(decl.ParametersJsonSchema)
			if params == nil && decl.Parameters != nil {
				params = schemaToOpenAIJSON(decl.Parameters)
			}
			if params == nil {
				params = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			out = append(out, chatTool{
				Type: "function",
				Function: chatFunction{
					Name:        decl.Name,
					Description: decl.Description,
					Parameters:  params,
					Strict:      strict,
				},
			})
		}
	}
	return out
}

func schemaToOpenAIJSON(schema *genai.Schema) map[string]any {
	if schema == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	out := map[string]any{}
	if schema.Type != "" {
		out["type"] = strings.ToLower(string(schema.Type))
	}
	if schema.Description != "" {
		out["description"] = schema.Description
	}
	if len(schema.Required) > 0 {
		out["required"] = schema.Required
	}
	if len(schema.Properties) > 0 {
		props := map[string]any{}
		for name, prop := range schema.Properties {
			props[name] = schemaToOpenAIJSON(prop)
		}
		out["properties"] = props
	}
	if schema.Items != nil {
		out["items"] = schemaToOpenAIJSON(schema.Items)
	}
	if len(out) == 0 {
		out["type"] = "object"
		out["properties"] = map[string]any{}
	}
	return out
}

func responseToLLM(resp *chatCompletionResponse) *model.LLMResponse {
	if resp == nil || len(resp.Choices) == 0 {
		return &model.LLMResponse{ErrorCode: "EMPTY_RESPONSE", ErrorMessage: "OpenAI-compatible response has no choices"}
	}
	choice := resp.Choices[0]
	parts := messageParts(choice.Message)
	return &model.LLMResponse{
		Content:       &genai.Content{Role: genai.RoleModel, Parts: parts},
		UsageMetadata: usageToGenAI(resp.Usage),
		ModelVersion:  firstNonEmpty(resp.Model, resp.SystemFingerprint),
		FinishReason:  finishReason(choice.FinishReason),
		CustomMetadata: map[string]any{
			"openai_id":      resp.ID,
			"openai_created": resp.Created,
		},
	}
}

func messageParts(msg chatMessage) []*genai.Part {
	var parts []*genai.Part
	if text := contentText(msg.Content); text != "" {
		parts = append(parts, genai.NewPartFromText(text))
	}
	for _, tc := range msg.ToolCalls {
		if tc.Type != "" && tc.Type != "function" {
			continue
		}
		args := map[string]any{}
		if strings.TrimSpace(tc.Function.Arguments) != "" {
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				args = map[string]any{"_raw": tc.Function.Arguments}
			}
		}
		parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{ID: tc.ID, Name: tc.Function.Name, Args: args}})
	}
	return parts
}

func usageToGenAI(u *chatUsage) *genai.GenerateContentResponseUsageMetadata {
	if u == nil {
		return nil
	}
	return &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:     int32(u.PromptTokens),
		CandidatesTokenCount: int32(u.CompletionTokens),
		TotalTokenCount:      int32(u.TotalTokens),
	}
}

func finishReason(fr string) genai.FinishReason {
	switch fr {
	case "stop":
		return genai.FinishReasonStop
	case "length":
		return genai.FinishReason("MAX_TOKENS")
	case "content_filter":
		return genai.FinishReason("SAFETY")
	case "tool_calls", "function_call":
		return genai.FinishReason("STOP")
	default:
		if fr == "" {
			return ""
		}
		return genai.FinishReason(strings.ToUpper(fr))
	}
}

func roleToOpenAI(role string) string {
	switch role {
	case "model":
		return "assistant"
	case "user", "system", "assistant", "tool":
		return role
	default:
		return "user"
	}
}

func dataURL(mimeType string, data []byte) string {
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func contentText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []chatContentPart:
		var b strings.Builder
		for _, p := range c {
			if p.Text != "" {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	case []any:
		var b strings.Builder
		for _, item := range c {
			if m, ok := item.(map[string]any); ok {
				if txt, ok := m["text"].(string); ok {
					b.WriteString(txt)
				}
			}
		}
		return b.String()
	default:
		return ""
	}
}

func decodeError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var oe openAIErrorResponse
	if err := json.Unmarshal(body, &oe); err == nil && oe.Error.Message != "" {
		return fmt.Errorf("openai-compatible API error: status=%d type=%s code=%v message=%s", resp.StatusCode, oe.Error.Type, oe.Error.Code, oe.Error.Message)
	}
	return fmt.Errorf("openai-compatible API error: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
}

func normalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", errors.New("empty openai base URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid openai base URL %q", raw)
	}
	return raw, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func cloneHeader(h http.Header) http.Header {
	out := make(http.Header)
	for k, values := range h {
		out[k] = append([]string(nil), values...)
	}
	return out
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func guessMimeType(fileName string) string {
	if mt := mime.TypeByExtension(filepath.Ext(fileName)); mt != "" {
		return mt
	}
	return "application/octet-stream"
}
