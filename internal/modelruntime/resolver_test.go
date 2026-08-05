package modelruntime

import (
	"context"
	"testing"

	"google.golang.org/adk/internal/runtimeplan"
)

func TestNewModelResolvesHubOpenAICompatibleSpec(t *testing.T) {
	llm, ref, err := NewModel(context.Background(), runtimeplan.ModelSpec{
		Profile: "coding-default", Provider: "openai_compatible",
		Model: "test-model", BaseURL: "http://127.0.0.1:1/v1",
	})
	if err != nil {
		t.Fatalf("NewModel() error = %v", err)
	}
	if llm == nil {
		t.Fatal("llm = nil")
	}
	if ref != "hub:coding-default" {
		t.Fatalf("ref = %q, want hub:coding-default", ref)
	}
}

func TestModelSpecFromHubFallsBackWhenEmpty(t *testing.T) {
	_, _, ok := modelSpecFromHub(runtimeplan.ModelSpec{})
	if ok {
		t.Fatal("ok = true, want false")
	}
}
