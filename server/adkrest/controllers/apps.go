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

package controllers

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/internal/aihubruntime"
	"gopkg.in/yaml.v3"
)

// AppsAPIController is the controller for the Apps API.
type AppsAPIController struct {
	agentLoader agent.Loader
	appRoots    []string
}

type requestScopedAgentCatalog interface {
	ListAgentsForRequest(ctx context.Context) ([]string, error)
	HubManaged() bool
}

// NewAppsAPIController creates a controller for Apps API.
func NewAppsAPIController(agentLoader agent.Loader, appRoots ...string) *AppsAPIController {
	return &AppsAPIController{agentLoader: agentLoader, appRoots: appRoots}
}

// AppListItem is the metadata-aware shape returned by /list-apps?include_meta=true.
// The plain /list-apps response stays []string for backwards compatibility.
type AppListItem struct {
	Name        string         `json:"name"`
	DisplayName string         `json:"display_name,omitempty"`
	Description string         `json:"description,omitempty"`
	AgentClass  string         `json:"agent_class,omitempty"`
	Model       string         `json:"model,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type appRootYAML struct {
	Name        string         `yaml:"name"`
	DisplayName string         `yaml:"display_name"`
	Title       string         `yaml:"title"`
	Label       string         `yaml:"label"`
	Description string         `yaml:"description"`
	AgentClass  string         `yaml:"agent_class"`
	Model       string         `yaml:"model"`
	Metadata    map[string]any `yaml:"metadata"`
}

// ListAppsHandler handles listing all loaded agents.
func (c *AppsAPIController) ListAppsHandler(rw http.ResponseWriter, req *http.Request) {
	includeMeta := req.URL.Query().Get("include_meta") == "true" || req.URL.Query().Get("format") == "metadata"

	ctx := aihubruntime.WithRequestHeaders(aihubruntime.WithCookieHeader(req.Context(), req.Header.Get("Cookie")), req.Header)
	seen := map[string]bool{}
	itemsByName := map[string]AppListItem{}
	apps := []string{}
	loaderNames := c.agentLoader.ListAgents()
	hubManaged := false
	if loader, ok := c.agentLoader.(requestScopedAgentCatalog); ok {
		var err error
		loaderNames, err = loader.ListAgentsForRequest(ctx)
		if err != nil {
			http.Error(rw, "failed to list authorized Hub agents: "+err.Error(), http.StatusBadGateway)
			return
		}
		hubManaged = loader.HubManaged()
	}
	for _, name := range loaderNames {
		if shouldHideAppName(name) || seen[name] {
			continue
		}
		seen[name] = true
		apps = append(apps, name)
		itemsByName[name] = AppListItem{Name: name}
	}

	// The embedded ADK WebUI builder can create apps as yaml files before they
	// are loaded into the runtime AgentLoader. Include filesystem app directories
	// so the UI can display manually created apps and builder tmp apps. Running
	// an app still requires it to be loaded by restarting or a future reload API.
	for _, root := range c.appRoots {
		if hubManaged {
			break
		}
		for _, item := range listAppItemsFromRoot(root) {
			if shouldHideAppName(item.Name) || seen[item.Name] {
				if existing, ok := itemsByName[item.Name]; ok {
					itemsByName[item.Name] = mergeAppListItem(existing, item)
				}
				continue
			}
			seen[item.Name] = true
			apps = append(apps, item.Name)
			itemsByName[item.Name] = item
		}
	}

	if !includeMeta {
		sort.Strings(apps)
		EncodeJSONResponse(apps, http.StatusOK, rw)
		return
	}

	items := make([]AppListItem, 0, len(apps))
	for _, name := range apps {
		item := itemsByName[name]
		if item.Name == "" {
			item.Name = name
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		left := strings.ToLower(firstNonEmptyAppString(items[i].DisplayName, items[i].Name))
		right := strings.ToLower(firstNonEmptyAppString(items[j].DisplayName, items[j].Name))
		if left == right {
			return items[i].Name < items[j].Name
		}
		return left < right
	})
	EncodeJSONResponse(items, http.StatusOK, rw)
}

func shouldHideAppName(name string) bool {
	return name == "" || strings.HasPrefix(name, "__adk_")
}

func listAppNamesFromRoot(root string) []string {
	items := listAppItemsFromRoot(root)
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Name)
	}
	return out
}

func listAppItemsFromRoot(root string) []AppListItem {
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := []AppListItem{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		rootAgentPath := filepath.Join(root, name, "root_agent.yaml")
		if _, err := os.Stat(rootAgentPath); err != nil {
			continue
		}
		item := readAppListItemFromRootAgent(name, rootAgentPath)
		if appMetadataHidden(item.Metadata) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func readAppListItemFromRootAgent(appName, path string) AppListItem {
	item := AppListItem{Name: appName}
	data, err := os.ReadFile(path)
	if err != nil {
		return item
	}
	var cfg appRootYAML
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return item
	}
	item.DisplayName = firstNonEmptyAppString(
		cfg.DisplayName,
		stringFromMetadata(cfg.Metadata, "display_name"),
		stringFromMetadata(cfg.Metadata, "meta_name"),
		stringFromMetadata(cfg.Metadata, "name_zh"),
		stringFromMetadata(cfg.Metadata, "zh_name"),
		cfg.Title,
		cfg.Label,
		stringFromMetadata(cfg.Metadata, "title"),
		stringFromMetadata(cfg.Metadata, "label"),
	)
	item.Description = cfg.Description
	item.AgentClass = cfg.AgentClass
	item.Model = cfg.Model
	item.Metadata = cfg.Metadata
	return item
}

func appMetadataHidden(metadata map[string]any) bool {
	if metadata == nil {
		return false
	}
	if hidden, ok := metadata["hidden"].(bool); ok {
		return hidden
	}
	if hidden, ok := metadata["hide"].(bool); ok {
		return hidden
	}
	return false
}

func stringFromMetadata(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	if v, ok := metadata[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func mergeAppListItem(base, overlay AppListItem) AppListItem {
	if base.DisplayName == "" {
		base.DisplayName = overlay.DisplayName
	}
	if base.Description == "" {
		base.Description = overlay.Description
	}
	if base.AgentClass == "" {
		base.AgentClass = overlay.AgentClass
	}
	if base.Model == "" {
		base.Model = overlay.Model
	}
	if base.Metadata == nil {
		base.Metadata = overlay.Metadata
	}
	return base
}

func firstNonEmptyAppString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
