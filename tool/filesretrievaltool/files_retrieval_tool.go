// Copyright 2026 Google LLC
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

// Package filesretrievaltool defines a lightweight local file retrieval tool.
// It searches text-like artifacts in the current workspace without depending on
// Google/Vertex services. It is intentionally simple and can later be replaced
// by a vector store backed implementation.
package filesretrievaltool

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"google.golang.org/genai"

	"google.golang.org/adk/internal/artifactfilter"
	"google.golang.org/adk/internal/toolinternal/toolutils"
	"google.golang.org/adk/internal/utils"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

const defaultTopK = 5

// New creates a local keyword based retrieval tool over artifacts.
func New() tool.Tool {
	return &filesRetrievalTool{}
}

type filesRetrievalTool struct{}

func (t *filesRetrievalTool) Name() string { return "files_retrieval" }

func (t *filesRetrievalTool) Description() string {
	return "Searches text-like files in the current artifact workspace and returns the most relevant snippets."
}

func (t *filesRetrievalTool) IsLongRunning() bool { return false }

func (t *filesRetrievalTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"query": {
					Type:        genai.TypeString,
					Description: "Search query describing the file content needed.",
				},
				"top_k": {
					Type:        genai.TypeInteger,
					Description: "Maximum number of snippets to return. Defaults to 5.",
				},
			},
			Required: []string{"query"},
		},
	}
}

func (t *filesRetrievalTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	if err := toolutils.PackTool(req, t); err != nil {
		return err
	}
	utils.AppendInstructions(req, "Use files_retrieval when the user asks about uploaded files, generated files, project files, or when you need to find relevant content without knowing the exact artifact file name. The tool searches the current artifact workspace and returns snippets; use load_artifacts when you need the complete content of a known file.")
	return nil
}

func (t *filesRetrievalTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	m, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected args type, got: %T", args)
	}
	query, err := requiredString(m, "query")
	if err != nil {
		return nil, err
	}
	topK := optionalInt(m, "top_k", defaultTopK)
	if topK <= 0 {
		topK = defaultTopK
	}
	if topK > 20 {
		topK = 20
	}

	listResp, err := ctx.Artifacts().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	if listResp == nil || len(listResp.FileNames) == 0 {
		return map[string]any{"query": query, "chunks": []map[string]any{}, "count": 0}, nil
	}

	queryTokens := tokenize(query)
	hits := []retrievalHit{}
	for _, fileName := range artifactfilter.VisibleFileNames(listResp.FileNames) {
		loadResp, err := ctx.Artifacts().Load(ctx, fileName)
		if err != nil || loadResp == nil || loadResp.Part == nil {
			continue
		}
		text := partText(loadResp.Part)
		if strings.TrimSpace(text) == "" {
			continue
		}
		score := scoreText(queryTokens, text)
		if score == 0 && len(queryTokens) > 0 {
			continue
		}
		hits = append(hits, retrievalHit{FileName: fileName, Text: snippet(text, queryTokens, 800), Score: score})
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].FileName < hits[j].FileName
		}
		return hits[i].Score > hits[j].Score
	})
	if len(hits) > topK {
		hits = hits[:topK]
	}

	chunks := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		chunks = append(chunks, map[string]any{"file_name": h.FileName, "score": h.Score, "text": h.Text})
	}
	return map[string]any{"query": query, "chunks": chunks, "count": len(chunks)}, nil
}

type retrievalHit struct {
	FileName string
	Text     string
	Score    int
}

func requiredString(m map[string]any, key string) (string, error) {
	v, ok := m[key]
	if !ok || v == nil {
		return "", fmt.Errorf("missing required argument %q", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("argument %q must be a string", key)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("argument %q cannot be empty", key)
	}
	return s, nil
}

func optionalInt(m map[string]any, key string, fallback int) int {
	v, ok := m[key]
	if !ok || v == nil {
		return fallback
	}
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, err := n.Int64()
		if err == nil {
			return int(i)
		}
	}
	return fallback
}

func partText(part *genai.Part) string {
	if part == nil {
		return ""
	}
	if part.Text != "" {
		return part.Text
	}
	if part.InlineData == nil {
		return ""
	}
	mimeType := strings.ToLower(part.InlineData.MIMEType)
	if !strings.HasPrefix(mimeType, "text/") && !strings.Contains(mimeType, "json") && !strings.Contains(mimeType, "yaml") && !strings.Contains(mimeType, "xml") && !strings.Contains(mimeType, "csv") {
		return ""
	}
	return string(part.InlineData.Data)
}

func tokenize(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_'
	})
	seen := map[string]bool{}
	out := []string{}
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

func scoreText(tokens []string, text string) int {
	lower := strings.ToLower(text)
	score := 0
	for _, token := range tokens {
		if token == "" {
			continue
		}
		score += strings.Count(lower, token)
	}
	return score
}

func snippet(text string, tokens []string, maxLen int) string {
	text = strings.TrimSpace(text)
	if len(text) <= maxLen {
		return text
	}
	lower := strings.ToLower(text)
	idx := -1
	for _, token := range tokens {
		if token == "" {
			continue
		}
		if i := strings.Index(lower, strings.ToLower(token)); i >= 0 && (idx < 0 || i < idx) {
			idx = i
		}
	}
	if idx < 0 {
		return strings.TrimSpace(text[:maxLen])
	}
	start := idx - maxLen/3
	if start < 0 {
		start = 0
	}
	end := start + maxLen
	if end > len(text) {
		end = len(text)
		start = end - maxLen
		if start < 0 {
			start = 0
		}
	}
	prefix := ""
	if start > 0 {
		prefix = "..."
	}
	suffix := ""
	if end < len(text) {
		suffix = "..."
	}
	return prefix + strings.TrimSpace(text[start:end]) + suffix
}

var _ tool.Tool = (*filesRetrievalTool)(nil)
