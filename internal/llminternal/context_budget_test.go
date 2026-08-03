package llminternal

import (
	"strings"
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/model"
)

func TestValidateLLMRequestContextBudgetRejectsOversizedRequest(t *testing.T) {
	req := &model.LLMRequest{
		Contents: genai.Text(strings.Repeat("x", 5000)),
		Config: &genai.GenerateContentConfig{
			MaxOutputTokens: 1000,
		},
	}

	err := validateLLMRequestContextBudget(req, 1000)
	if err == nil {
		t.Fatal("validateLLMRequestContextBudget() error = nil, want oversized request error")
	}
	if !strings.Contains(err.Error(), "context budget exceeded") {
		t.Fatalf("error = %v, want context budget exceeded", err)
	}
}

func TestValidateLLMRequestContextBudgetAllowsSmallRequest(t *testing.T) {
	req := &model.LLMRequest{
		Contents: genai.Text("small request"),
		Config: &genai.GenerateContentConfig{
			MaxOutputTokens: 100,
		},
	}

	if err := validateLLMRequestContextBudget(req, 1000); err != nil {
		t.Fatalf("validateLLMRequestContextBudget() error = %v, want nil", err)
	}
}
