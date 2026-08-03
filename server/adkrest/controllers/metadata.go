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

package controllers

import (
	"net/http"
	"runtime"

	"google.golang.org/adk/internal/runtimeconfig"
	"google.golang.org/adk/internal/version"
)

// MetadataAPIController exposes runtime metadata that the WebUI Builder should
// not hardcode, such as configured models, available tools, and builder defaults.
type MetadataAPIController struct {
	cfg *runtimeconfig.Config
}

// NewMetadataAPIController creates a metadata controller.
func NewMetadataAPIController(cfg *runtimeconfig.Config) *MetadataAPIController {
	return &MetadataAPIController{cfg: cfg}
}

func (c *MetadataAPIController) config() *runtimeconfig.Config {
	if c != nil && c.cfg != nil {
		return c.cfg
	}
	return runtimeconfig.FromContext(nil)
}

// ModelsHandler handles GET /models.
func (c *MetadataAPIController) ModelsHandler(rw http.ResponseWriter, req *http.Request) {
	cfg := c.config()
	EncodeJSONResponse(map[string]any{
		"default": cfg.Models.Default,
		"models":  cfg.FrontendModels(),
	}, http.StatusOK, rw)
}

// VersionHandler handles GET /version.
func (c *MetadataAPIController) VersionHandler(rw http.ResponseWriter, req *http.Request) {
	EncodeJSONResponse(map[string]any{
		"version":          version.Version,
		"language":         "go",
		"language_version": runtime.Version(),
	}, http.StatusOK, rw)
}

// ToolsHandler handles GET /tools.
func (c *MetadataAPIController) ToolsHandler(rw http.ResponseWriter, req *http.Request) {
	cfg := c.config()
	EncodeJSONResponse(map[string]any{
		"tools": cfg.FrontendTools(),
	}, http.StatusOK, rw)
}

// BuilderDefaultsHandler handles GET /builder/defaults.
func (c *MetadataAPIController) BuilderDefaultsHandler(rw http.ResponseWriter, req *http.Request) {
	cfg := c.config()
	EncodeJSONResponse(cfg.FrontendBuilderDefaults(), http.StatusOK, rw)
}

// UploadsHandler handles GET /uploads/config.
func (c *MetadataAPIController) UploadsHandler(rw http.ResponseWriter, req *http.Request) {
	cfg := c.config()
	EncodeJSONResponse(cfg.FrontendUploadConfig(), http.StatusOK, rw)
}
