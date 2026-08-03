package llminternal

import (
	"context"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"google.golang.org/genai"

	"google.golang.org/adk/internal/runtimetrace"
	"google.golang.org/adk/model"
)

const defaultEstimatedOutputTokens int64 = 4096
const defaultLLMContextWindow int64 = 128000

func validateLLMRequestContextBudget(req *model.LLMRequest, contextWindow int64) error {
	if req == nil || contextWindow <= 0 {
		return nil
	}
	inputChars, estimatedInputTokens, outputTokens := estimateLLMRequestBudget(req)
	if estimatedInputTokens+outputTokens <= contextWindow {
		return nil
	}
	return fmt.Errorf("context budget exceeded before model call: estimated_input_tokens=%d max_output_tokens=%d context_window=%d input_chars=%d; reduce loaded artifacts, batch size, or output token limit", estimatedInputTokens, outputTokens, contextWindow, inputChars)
}

func estimateLLMRequestBudget(req *model.LLMRequest) (inputChars int, estimatedInputTokens int64, outputTokens int64) {
	if req == nil {
		return 0, 0, defaultEstimatedOutputTokens
	}
	inputChars = estimateLLMRequestChars(req)
	estimatedInputTokens = int64(inputChars/3 + 1)
	outputTokens = defaultEstimatedOutputTokens
	if req.Config != nil && req.Config.MaxOutputTokens > 0 {
		outputTokens = int64(req.Config.MaxOutputTokens)
	}
	return inputChars, estimatedInputTokens, outputTokens
}

func traceLLMRequestBudget(ctx context.Context, req *model.LLMRequest, contextWindow int64, budgetErr error) {
	if !traceEnabled(ctx) || req == nil {
		return
	}
	inputChars, estimatedInputTokens, outputTokens := estimateLLMRequestBudget(req)
	data := map[string]any{
		"model":                  req.Model,
		"content_count":          len(req.Contents),
		"input_chars":            inputChars,
		"estimated_input_tokens": estimatedInputTokens,
		"max_output_tokens":      outputTokens,
		"context_window":         contextWindow,
		"estimated_total_tokens": estimatedInputTokens + outputTokens,
		"over_budget":            estimatedInputTokens+outputTokens > contextWindow,
	}
	if req.Config != nil {
		data["tool_count"] = countFunctionDeclarations(req.Config)
	}
	if budgetErr != nil {
		data["error"] = budgetErr.Error()
	}
	runtimetrace.Record(ctx, "llm.request.budget_check", data)
}

func estimateLLMRequestChars(req *model.LLMRequest) int {
	if req == nil {
		return 0
	}
	total := 0
	if req.Config != nil {
		total += estimateContentChars(req.Config.SystemInstruction)
		if len(req.Config.Tools) > 0 {
			if data, err := json.Marshal(req.Config.Tools); err == nil {
				total += len(data)
			}
		}
		if req.Config.ResponseSchema != nil {
			if data, err := json.Marshal(req.Config.ResponseSchema); err == nil {
				total += len(data)
			}
		}
	}
	for _, content := range req.Contents {
		total += estimateContentChars(content)
	}
	return total
}

func estimateContentChars(content *genai.Content) int {
	if content == nil {
		return 0
	}
	total := 0
	for _, part := range content.Parts {
		if part == nil {
			continue
		}
		total += utf8.RuneCountInString(part.Text)
		if part.InlineData != nil {
			total += len(part.InlineData.Data)
		}
		if part.FunctionCall != nil {
			if data, err := json.Marshal(part.FunctionCall); err == nil {
				total += len(data)
			}
		}
		if part.FunctionResponse != nil {
			if data, err := json.Marshal(part.FunctionResponse); err == nil {
				total += len(data)
			}
		}
	}
	return total
}
