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

package openai

import (
	"encoding/json"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/model"
)

type streamAccumulator struct {
	id                string
	model             string
	systemFingerprint string
	usage             *chatUsage
	text              strings.Builder
	toolCalls         map[int]*toolCallBuilder
	finishReason      string
	sentFinal         bool
}

type toolCallBuilder struct {
	id        string
	typ       string
	name      string
	arguments strings.Builder
}

func (a *streamAccumulator) add(chunk *chatCompletionChunk) ([]*model.LLMResponse, *model.LLMResponse) {
	if chunk == nil {
		return nil, nil
	}
	if a.toolCalls == nil {
		a.toolCalls = make(map[int]*toolCallBuilder)
	}
	if chunk.ID != "" {
		a.id = chunk.ID
	}
	if chunk.Model != "" {
		a.model = chunk.Model
	}
	if chunk.SystemFingerprint != "" {
		a.systemFingerprint = chunk.SystemFingerprint
	}
	if chunk.Usage != nil {
		a.usage = chunk.Usage
	}

	var partials []*model.LLMResponse
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" {
			a.text.WriteString(choice.Delta.Content)
			partials = append(partials, &model.LLMResponse{
				Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{genai.NewPartFromText(choice.Delta.Content)}},
				Partial: true,
			})
		}
		for _, tc := range choice.Delta.ToolCalls {
			b := a.toolCalls[tc.Index]
			if b == nil {
				b = &toolCallBuilder{}
				a.toolCalls[tc.Index] = b
			}
			if tc.ID != "" {
				b.id = tc.ID
			}
			if tc.Type != "" {
				b.typ = tc.Type
			}
			if tc.Function.Name != "" {
				b.name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				b.arguments.WriteString(tc.Function.Arguments)
			}
		}
		if choice.FinishReason != "" {
			a.finishReason = choice.FinishReason
		}
	}
	if a.finishReason != "" {
		return partials, a.final()
	}
	return partials, nil
}

func (a *streamAccumulator) final() *model.LLMResponse {
	if a == nil || a.sentFinal {
		return nil
	}
	if a.finishReason == "" && a.text.Len() == 0 && len(a.toolCalls) == 0 {
		return nil
	}
	a.sentFinal = true

	var parts []*genai.Part
	if a.text.Len() > 0 {
		parts = append(parts, genai.NewPartFromText(a.text.String()))
	}
	for i := 0; i < len(a.toolCalls); i++ {
		b := a.toolCalls[i]
		if b == nil {
			continue
		}
		args := map[string]any{}
		if strings.TrimSpace(b.arguments.String()) != "" {
			if err := json.Unmarshal([]byte(b.arguments.String()), &args); err != nil {
				args = map[string]any{"_raw": b.arguments.String()}
			}
		}
		parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{ID: b.id, Name: b.name, Args: args}})
	}
	return &model.LLMResponse{
		Content:       &genai.Content{Role: genai.RoleModel, Parts: parts},
		UsageMetadata: usageToGenAI(a.usage),
		ModelVersion:  firstNonEmpty(a.model, a.systemFingerprint),
		FinishReason:  finishReason(a.finishReason),
		TurnComplete:  true,
		CustomMetadata: map[string]any{
			"openai_id": a.id,
		},
	}
}
