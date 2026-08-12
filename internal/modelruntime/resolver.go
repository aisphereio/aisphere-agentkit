// Package modelruntime resolves Hub model snapshots into ADK model adapters.
package modelruntime

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"google.golang.org/adk/internal/runtimeconfig"
	"google.golang.org/adk/internal/runtimeplan"
	"google.golang.org/adk/model"
)

func NewModel(ctx context.Context, spec runtimeplan.ModelSpec) (model.LLM, string, error) {
	ref, cfgSpec, ok := modelSpecFromHub(spec)
	if !ok {
		return runtimeconfig.NewModel(ctx, "")
	}
	base := runtimeconfig.FromContext(ctx)
	cfgCopy := *base
	cfgCopy.Models.Specs = maps.Clone(base.Models.Specs)
	if cfgCopy.Models.Specs == nil {
		cfgCopy.Models.Specs = map[string]runtimeconfig.ModelSpec{}
	}
	cfgCopy.Models.Specs[ref] = cfgSpec
	return runtimeconfig.NewModel(runtimeconfig.WithConfig(ctx, &cfgCopy), ref)
}

func modelSpecFromHub(spec runtimeplan.ModelSpec) (string, runtimeconfig.ModelSpec, bool) {
	modelName := strings.TrimSpace(spec.Model)
	provider := strings.TrimSpace(spec.Provider)
	apiFormat := strings.ToLower(strings.TrimSpace(spec.APIFormat))
	if provider == "" && apiFormat != "" {
		provider = apiFormat
	}
	if provider == "openai" && apiFormat == "openai-compatible" {
		provider = "openai_compatible"
	}
	if modelName == "" && strings.TrimSpace(spec.Profile) != "" {
		modelName = strings.TrimSpace(spec.Profile)
	}
	if modelName == "" && provider == "" && strings.TrimSpace(spec.BaseURL) == "" {
		return "", runtimeconfig.ModelSpec{}, false
	}
	ref := "hub:" + firstNonEmpty(spec.Profile, modelName, provider)
	headers := map[string]string{}
	if raw, ok := spec.Metadata["headers"].(map[string]any); ok {
		for key, value := range raw {
			if text := strings.TrimSpace(fmt.Sprint(value)); text != "" {
				headers[key] = text
			}
		}
	}
	out := runtimeconfig.ModelSpec{
		Provider: provider,
		Model:    modelName,
		BaseURL:  strings.TrimSpace(spec.BaseURL),
		Headers:  headers,
	}
	if value := metadataString(spec.Metadata, "apiKeyEnv"); value != "" {
		out.APIKeyEnv = value
	}
	if value := metadataString(spec.Metadata, "api_key_env"); value != "" {
		out.APIKeyEnv = value
	}
	if value := metadataString(spec.Metadata, "apiKey"); value != "" {
		out.APIKey = value
	}
	if value := metadataString(spec.Metadata, "credentialRef"); value != "" && out.APIKey == "" {
		// Hub resource-v2 snapshots carry the concrete credential under
		// credential_ref; surface it as the API key for the adapter.
		out.APIKey = value
	}
	if value := metadataString(spec.Metadata, "timeout"); value != "" {
		out.Timeout = value
	}
	if strict, ok := spec.Metadata["strictTools"].(bool); ok {
		out.StrictTools = strict
	}
	return ref, out, true
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "default"
}
