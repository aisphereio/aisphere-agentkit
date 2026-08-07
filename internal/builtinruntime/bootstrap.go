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

// Package builtinruntime owns trusted model-callable Builtin Tool implementations
// compiled into the AISphere Runtime binary.
package builtinruntime

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/genai"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/deleteartifactstool"
	"google.golang.org/adk/tool/getuserchoicetool"
	"google.golang.org/adk/tool/listartifactstool"
	"google.golang.org/adk/tool/loadartifactstool"
	"google.golang.org/adk/tool/loadmemorytool"
	"google.golang.org/adk/tool/requestuserformtool"
	"google.golang.org/adk/tool/saveartifactstool"
)

// builtinImplementationVersion is intentionally independent from Hub's
// ToolVersion label. It identifies the executable contract compiled into the
// Runtime binary.
const builtinImplementationVersion = "1"

type declarationTool interface {
	tool.Tool
	Declaration() *genai.FunctionDeclaration
}

func init() {
	mustRegisterDeclarationBuiltin("save_artifact", map[string]any{
		"domain": "artifact", "mutating": true,
	}, func(context.Context, map[string]any) (tool.Tool, error) {
		return saveartifactstool.New()
	})
	mustRegisterDeclarationBuiltin("load_artifacts", map[string]any{
		"domain": "artifact", "readOnly": true,
	}, func(context.Context, map[string]any) (tool.Tool, error) {
		return loadartifactstool.New(), nil
	})
	mustRegisterDeclarationBuiltin("list_artifacts", map[string]any{
		"domain": "artifact", "readOnly": true,
	}, func(context.Context, map[string]any) (tool.Tool, error) {
		return listartifactstool.New(), nil
	})
	mustRegisterDeclarationBuiltin("delete_artifact", map[string]any{
		"domain": "artifact", "mutating": true, "destructive": true,
	}, func(context.Context, map[string]any) (tool.Tool, error) {
		return deleteartifactstool.New(), nil
	})
	mustRegisterDeclarationBuiltin("load_memory", map[string]any{
		"domain": "memory", "readOnly": true,
	}, func(context.Context, map[string]any) (tool.Tool, error) {
		return loadmemorytool.New(), nil
	})
	mustRegisterDeclarationBuiltin("get_user_choice", map[string]any{
		"domain": "interaction", "readOnly": true,
	}, func(context.Context, map[string]any) (tool.Tool, error) {
		return getuserchoicetool.New()
	})
	mustRegisterDeclarationBuiltin("request_user_form", map[string]any{
		"domain": "interaction", "readOnly": true,
	}, func(context.Context, map[string]any) (tool.Tool, error) {
		return requestuserformtool.New()
	})
}

// mustRegisterDeclarationBuiltin migrates an existing code-owned ADK Tool into
// the AISphere BuiltinRegistry without duplicating its model schema. The Tool's
// Declaration remains the code source of truth; Hub receives only the derived
// descriptor manifest.
func mustRegisterDeclarationBuiltin(id string, annotations map[string]any, factory Factory) {
	probe, err := factory(context.Background(), nil)
	if err != nil {
		panic(fmt.Sprintf("register builtin %s: create descriptor probe: %v", id, err))
	}
	declTool, ok := probe.(declarationTool)
	if !ok {
		panic(fmt.Sprintf("register builtin %s: implementation has no FunctionDeclaration", id))
	}
	declaration := declTool.Declaration()
	if declaration == nil {
		panic(fmt.Sprintf("register builtin %s: implementation returned nil FunctionDeclaration", id))
	}
	inputSchema, err := schemaObject(declaration.Parameters)
	if err != nil {
		panic(fmt.Sprintf("register builtin %s: encode input schema: %v", id, err))
	}
	descriptor := Descriptor{
		ID:                    id,
		ImplementationVersion: builtinImplementationVersion,
		Model: ModelContract{
			Name:        declaration.Name,
			Description: declaration.Description,
			InputSchema: inputSchema,
		},
		Annotations: annotations,
	}
	if err := RegisterBuiltin(descriptor, factory); err != nil {
		panic(fmt.Sprintf("register builtin %s: %v", id, err))
	}
}

func schemaObject(schema *genai.Schema) (map[string]any, error) {
	if schema == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, err
	}
	return out, nil
}
