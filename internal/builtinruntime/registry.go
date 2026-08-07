// Package builtinruntime owns trusted Tool implementations compiled into the
// AISphere Runtime binary. Hub may mirror their descriptors in its Tool catalog,
// but executable code never moves from Hub to Runtime at run time.
package builtinruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"google.golang.org/adk/tool"
)

// Descriptor is the code-owned contract for one Runtime builtin implementation.
// It is safe to export as a manifest to Hub because it contains no executable
// payload and no credential value.
type Descriptor struct {
	ID                    string         `json:"id"`
	ImplementationVersion string         `json:"implementationVersion"`
	Model                 ModelContract  `json:"model"`
	Annotations           map[string]any `json:"annotations,omitempty"`
	Digest                string         `json:"digest,omitempty"`
}

// ModelContract is the only part of a builtin descriptor that is exposed to the
// model through ADK tool/function declarations.
type ModelContract struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"inputSchema,omitempty"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
}

// Manifest is the build/runtime capability document that can be mirrored into
// Hub's system Tool catalog. It deliberately carries descriptors only, never Go
// source, binaries, secrets, endpoints, or Runtime-local dependencies.
type Manifest struct {
	RuntimeVersion string       `json:"runtimeVersion,omitempty"`
	Builtins       []Descriptor `json:"builtins"`
}

// Factory creates the executable ADK Tool from Runtime-local dependencies and
// the binding arguments already validated by the Tool compiler.
type Factory func(context.Context, map[string]any) (tool.Tool, error)

type entry struct {
	descriptor Descriptor
	factory    Factory
}

// Registry is the Runtime-local implementation registry. It is an executable
// capability set, not the list of Tools exposed to an Agent. An Agent sees only
// the subset explicitly pinned in its ExecutionSpec/ExecutionSnapshot.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]entry
}

var processRegistry = NewRegistry()

func NewRegistry() *Registry {
	return &Registry{entries: map[string]entry{}}
}

// DefaultRegistry returns the Runtime process registry. New Builtin
// implementations migrate into this registry gradually; old configurable
// factories remain a compatibility path only until their descriptor is moved.
func DefaultRegistry() *Registry {
	return processRegistry
}

// RegisterBuiltin registers one code-owned implementation in the process
// registry. Runtime bootstrap code should call this for production Builtins.
func RegisterBuiltin(descriptor Descriptor, factory Factory) error {
	return processRegistry.Register(descriptor, factory)
}

// Register adds one exact builtin implementation. Duplicate id+version entries
// are rejected so Runtime startup fails closed instead of silently replacing an
// implementation behind an immutable Hub ToolVersion.
func (r *Registry) Register(descriptor Descriptor, factory Factory) error {
	if r == nil {
		return fmt.Errorf("builtin registry is nil")
	}
	if factory == nil {
		return fmt.Errorf("builtin factory is required")
	}
	descriptor.ID = strings.TrimSpace(descriptor.ID)
	descriptor.ImplementationVersion = strings.TrimSpace(descriptor.ImplementationVersion)
	descriptor.Model.Name = strings.TrimSpace(descriptor.Model.Name)
	if descriptor.ID == "" {
		return fmt.Errorf("builtin id is required")
	}
	if descriptor.ImplementationVersion == "" {
		return fmt.Errorf("builtin %q implementation version is required", descriptor.ID)
	}
	if descriptor.Model.Name == "" {
		return fmt.Errorf("builtin %q model name is required", descriptor.ID)
	}
	computed, err := descriptorDigest(descriptor)
	if err != nil {
		return fmt.Errorf("builtin %q descriptor digest: %w", descriptor.ID, err)
	}
	if descriptor.Digest != "" && !strings.EqualFold(strings.TrimSpace(descriptor.Digest), computed) {
		return fmt.Errorf("builtin %q descriptor digest mismatch", descriptor.ID)
	}
	descriptor.Digest = computed
	key := registryKey(descriptor.ID, descriptor.ImplementationVersion)

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = map[string]entry{}
	}
	if _, exists := r.entries[key]; exists {
		return fmt.Errorf("builtin %s is already registered", key)
	}
	r.entries[key] = entry{descriptor: cloneDescriptor(descriptor), factory: factory}
	return nil
}

// Has reports whether the registry can resolve the requested Builtin under the
// same version rules as Resolve. A blank version is resolvable only if exactly
// one implementation version exists locally.
func (r *Registry) Has(id, implementationVersion string) bool {
	if r == nil {
		return false
	}
	id = strings.TrimSpace(id)
	implementationVersion = strings.TrimSpace(implementationVersion)
	if id == "" {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if implementationVersion != "" {
		_, ok := r.entries[registryKey(id, implementationVersion)]
		return ok
	}
	count := 0
	for _, candidate := range r.entries {
		if candidate.descriptor.ID == id {
			count++
			if count > 1 {
				return false
			}
		}
	}
	return count == 1
}

// Resolve performs an exact implementation lookup. If implementationVersion is
// empty, resolution is allowed only when exactly one version of the requested
// builtin exists; otherwise callers must pin a version explicitly.
func (r *Registry) Resolve(ctx context.Context, id, implementationVersion string, args map[string]any) (tool.Tool, Descriptor, error) {
	if r == nil {
		return nil, Descriptor{}, fmt.Errorf("builtin registry is not configured")
	}
	id = strings.TrimSpace(id)
	implementationVersion = strings.TrimSpace(implementationVersion)
	if id == "" {
		return nil, Descriptor{}, fmt.Errorf("builtin id is required")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	selected, ok := r.entries[registryKey(id, implementationVersion)]
	if implementationVersion == "" {
		matches := make([]entry, 0, 1)
		for _, candidate := range r.entries {
			if candidate.descriptor.ID == id {
				matches = append(matches, candidate)
			}
		}
		switch len(matches) {
		case 0:
			return nil, Descriptor{}, fmt.Errorf("builtin implementation %q is not available", id)
		case 1:
			selected, ok = matches[0], true
		default:
			return nil, Descriptor{}, fmt.Errorf("builtin %q has multiple implementation versions; an exact version is required", id)
		}
	}
	if !ok {
		return nil, Descriptor{}, fmt.Errorf("builtin implementation %s is not available", registryKey(id, implementationVersion))
	}
	resolved, err := selected.factory(ctx, cloneMap(args))
	if err != nil {
		return nil, Descriptor{}, err
	}
	if resolved == nil {
		return nil, Descriptor{}, fmt.Errorf("builtin implementation %s returned nil tool", registryKey(selected.descriptor.ID, selected.descriptor.ImplementationVersion))
	}
	return resolved, cloneDescriptor(selected.descriptor), nil
}

// Manifest returns a deterministic descriptor-only view suitable for Hub
// reconciliation and observability. Registration order never affects output.
func (r *Registry) Manifest(runtimeVersion string) Manifest {
	manifest := Manifest{RuntimeVersion: strings.TrimSpace(runtimeVersion), Builtins: []Descriptor{}}
	if r == nil {
		return manifest
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, value := range r.entries {
		manifest.Builtins = append(manifest.Builtins, cloneDescriptor(value.descriptor))
	}
	sort.Slice(manifest.Builtins, func(i, j int) bool {
		if manifest.Builtins[i].ID == manifest.Builtins[j].ID {
			return manifest.Builtins[i].ImplementationVersion < manifest.Builtins[j].ImplementationVersion
		}
		return manifest.Builtins[i].ID < manifest.Builtins[j].ID
	})
	return manifest
}

func registryKey(id, version string) string {
	id = strings.TrimSpace(id)
	version = strings.TrimSpace(version)
	if version == "" {
		return id
	}
	return id + "@" + version
}

func descriptorDigest(descriptor Descriptor) (string, error) {
	copy := cloneDescriptor(descriptor)
	copy.Digest = ""
	encoded, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func cloneDescriptor(in Descriptor) Descriptor {
	out := in
	out.Model.InputSchema = cloneMap(in.Model.InputSchema)
	out.Model.OutputSchema = cloneMap(in.Model.OutputSchema)
	out.Annotations = cloneMap(in.Annotations)
	return out
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
