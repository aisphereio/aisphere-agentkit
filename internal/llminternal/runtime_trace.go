// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package llminternal

import (
	"context"
	"encoding/json"

	"google.golang.org/genai"

	"google.golang.org/adk/internal/runtimetrace"
	"google.golang.org/adk/internal/utils"
	"google.golang.org/adk/model"
)

func traceEnabled(ctx context.Context) bool {
	rec := runtimetrace.FromContext(ctx)
	return rec != nil && rec.Enabled()
}

func tracePreprocess(ctx context.Context, stage string, req *model.LLMRequest) {
	if !traceEnabled(ctx) || req == nil {
		return
	}

	runtimetrace.Record(ctx, stage, map[string]any{
		"model":         req.Model,
		"content_count": len(req.Contents),
		"tool_count":    countFunctionDeclarations(req.Config),
	})
}

func traceLLMRequest(ctx context.Context, req *model.LLMRequest, stream bool) {
	if !traceEnabled(ctx) || req == nil {
		return
	}

	tools := summarizeTools(req.Config)
	runtimetrace.Record(ctx, runtimetrace.EventModelCallStarted, map[string]any{
		"model":         req.Model,
		"stream":        stream,
		"content_count": len(req.Contents),
		"tool_count":    len(tools),
	})
	if len(tools) > 0 {
		runtimetrace.Record(ctx, runtimetrace.EventToolsBound, map[string]any{
			"model":      req.Model,
			"tool_count": len(tools),
			"tools":      tools,
		})
	}

	runtimetrace.Record(ctx, "llm.request.final", map[string]any{
		"model":    req.Model,
		"stream":   stream,
		"contents": summarizeContents(req.Contents),
		"config":   summarizeConfig(req.Config),
		"tools":    tools,
	})
}

func traceLLMResponse(ctx context.Context, resp *model.LLMResponse, err error) {
	if !traceEnabled(ctx) {
		return
	}

	data := map[string]any{}
	if err != nil {
		data["error"] = err.Error()
	}
	if resp != nil {
		data["partial"] = resp.Partial
		data["turn_complete"] = resp.TurnComplete
		data["finish_reason"] = resp.FinishReason
		data["model_version"] = resp.ModelVersion
		data["content"] = summarizeContent(resp.Content)
		data["function_calls"] = summarizeFunctionCalls(utils.FunctionCalls(resp.Content))
		data["usage"] = resp.UsageMetadata
		data["custom_metadata"] = resp.CustomMetadata
	}

	name := "llm.response.final"
	if resp != nil && resp.Partial {
		name = "llm.response.partial"
	}

	runtimetrace.Record(ctx, name, data)
	if err != nil {
		runtimetrace.Record(ctx, runtimetrace.EventModelCallError, data)
	} else if resp != nil && !resp.Partial {
		runtimetrace.Record(ctx, runtimetrace.EventModelCallCompleted, data)
	}
}

func traceToolCall(ctx context.Context, fnCall *genai.FunctionCall) {
	if !traceEnabled(ctx) || fnCall == nil {
		return
	}

	data := map[string]any{
		"id":   fnCall.ID,
		"name": fnCall.Name,
		"args": fnCall.Args,
	}
	runtimetrace.Record(ctx, "tool.call.detected", data)
	runtimetrace.Record(ctx, runtimetrace.EventToolCall, data)
}

func traceToolResult(ctx context.Context, fnCall *genai.FunctionCall, result map[string]any, err error) {
	if !traceEnabled(ctx) || fnCall == nil {
		return
	}

	data := map[string]any{
		"id":     fnCall.ID,
		"name":   fnCall.Name,
		"result": result,
	}
	if err != nil {
		data["error"] = err.Error()
	}

	runtimetrace.Record(ctx, "tool.result", data)
	if err != nil {
		runtimetrace.Record(ctx, runtimetrace.EventToolError, data)
	} else {
		runtimetrace.Record(ctx, runtimetrace.EventToolResult, data)
	}
}

func countFunctionDeclarations(cfg *genai.GenerateContentConfig) int {
	if cfg == nil {
		return 0
	}

	n := 0
	for _, t := range cfg.Tools {
		if t != nil {
			n += len(t.FunctionDeclarations)
		}
	}
	return n
}

func summarizeConfig(cfg *genai.GenerateContentConfig) map[string]any {
	if cfg == nil {
		return nil
	}

	return map[string]any{
		"temperature":         cfg.Temperature,
		"top_p":               cfg.TopP,
		"top_k":               cfg.TopK,
		"max_output_tokens":   cfg.MaxOutputTokens,
		"candidate_count":     cfg.CandidateCount,
		"response_mime_type":  cfg.ResponseMIMEType,
		"has_response_schema": cfg.ResponseSchema != nil,
		"system_instruction":  summarizeContent(cfg.SystemInstruction),
	}
}

func summarizeTools(cfg *genai.GenerateContentConfig) []map[string]any {
	if cfg == nil {
		return nil
	}

	out := []map[string]any{}
	for _, t := range cfg.Tools {
		if t == nil {
			continue
		}

		for _, decl := range t.FunctionDeclarations {
			if decl == nil {
				continue
			}

			params := any(decl.ParametersJsonSchema)
			if params == nil {
				params = decl.Parameters
			}

			out = append(out, map[string]any{
				"name":        decl.Name,
				"description": decl.Description,
				"parameters":  params,
			})
		}
	}

	return out
}

func summarizeContents(contents []*genai.Content) []map[string]any {
	out := make([]map[string]any, 0, len(contents))
	for _, content := range contents {
		out = append(out, summarizeContent(content))
	}
	return out
}

func summarizeContent(content *genai.Content) map[string]any {
	if content == nil {
		return nil
	}

	parts := make([]map[string]any, 0, len(content.Parts))
	for _, part := range content.Parts {
		parts = append(parts, summarizePart(part))
	}

	return map[string]any{
		"role":  content.Role,
		"parts": parts,
	}
}

func summarizePart(part *genai.Part) map[string]any {
	if part == nil {
		return nil
	}

	out := map[string]any{}

	if part.Text != "" {
		out["text"] = part.Text
	}
	if part.FunctionCall != nil {
		out["function_call"] = map[string]any{
			"id":   part.FunctionCall.ID,
			"name": part.FunctionCall.Name,
			"args": part.FunctionCall.Args,
		}
	}
	if part.FunctionResponse != nil {
		out["function_response"] = map[string]any{
			"id":       part.FunctionResponse.ID,
			"name":     part.FunctionResponse.Name,
			"response": part.FunctionResponse.Response,
		}
	}
	if part.InlineData != nil {
		out["inline_data"] = map[string]any{
			"mime_type": part.InlineData.MIMEType,
			"bytes":     len(part.InlineData.Data),
		}
	}
	if part.FileData != nil {
		out["file_data"] = part.FileData
	}

	if len(out) == 0 {
		b, _ := json.Marshal(part)
		out["raw"] = string(b)
	}

	return out
}

func summarizeFunctionCalls(calls []*genai.FunctionCall) []map[string]any {
	out := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		if call == nil {
			continue
		}

		out = append(out, map[string]any{
			"id":   call.ID,
			"name": call.Name,
			"args": call.Args,
		})
	}
	return out
}
