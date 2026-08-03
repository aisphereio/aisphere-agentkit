// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package routers

import (
	"net/http"

	"google.golang.org/adk/server/adkrest/controllers"
)

type SkillsAPIRouter struct {
	controller *controllers.SkillsAPIController
}

func NewSkillsAPIRouter(controller *controllers.SkillsAPIController) *SkillsAPIRouter {
	return &SkillsAPIRouter{controller: controller}
}

func (r *SkillsAPIRouter) Routes() Routes {
	return Routes{
		Route{Name: "ListSkills", Methods: []string{http.MethodGet}, Pattern: "/skills", HandlerFunc: r.controller.ListSkillsHandler},
		Route{Name: "GetSkill", Methods: []string{http.MethodGet}, Pattern: "/skills/{name}", HandlerFunc: r.controller.GetSkillHandler},
		Route{Name: "CreateSkill", Methods: []string{http.MethodPost}, Pattern: "/skills", HandlerFunc: r.controller.CreateSkillHandler},
		Route{Name: "ImportSkill", Methods: []string{http.MethodPost}, Pattern: "/skills/import", HandlerFunc: r.controller.ImportSkillHandler},
		Route{Name: "UpdateSkill", Methods: []string{http.MethodPut}, Pattern: "/skills/{name}", HandlerFunc: r.controller.UpdateSkillHandler},
		Route{Name: "DeleteSkill", Methods: []string{http.MethodDelete}, Pattern: "/skills/{name}", HandlerFunc: r.controller.DeleteSkillHandler},
		Route{Name: "ValidateSkill", Methods: []string{http.MethodPost}, Pattern: "/skills/{name}/validate", HandlerFunc: r.controller.ValidateSkillHandler},
		Route{Name: "SkillReferences", Methods: []string{http.MethodGet}, Pattern: "/skills/{name}/references", HandlerFunc: r.controller.SkillReferencesHandler},
		Route{Name: "UpdateSkillStatus", Methods: []string{http.MethodPost}, Pattern: "/skills/{name}/status", HandlerFunc: r.controller.UpdateSkillStatusHandler},
		Route{Name: "PublishSkill", Methods: []string{http.MethodPost}, Pattern: "/skills/{name}/publish", HandlerFunc: r.controller.PublishSkillHandler},
		Route{Name: "DeprecateSkill", Methods: []string{http.MethodPost}, Pattern: "/skills/{name}/deprecate", HandlerFunc: r.controller.DeprecateSkillHandler},
		Route{Name: "ArchiveSkill", Methods: []string{http.MethodPost}, Pattern: "/skills/{name}/archive", HandlerFunc: r.controller.ArchiveSkillHandler},
		Route{Name: "ExportSkill", Methods: []string{http.MethodGet}, Pattern: "/skills/{name}/export", HandlerFunc: r.controller.ExportSkillHandler},
		Route{Name: "ListSkillResources", Methods: []string{http.MethodGet}, Pattern: "/skills/{name}/resources", HandlerFunc: r.controller.ListSkillResourcesHandler},
		Route{Name: "SaveSkillResource", Methods: []string{http.MethodPost, http.MethodPut}, Pattern: "/skills/{name}/resources", HandlerFunc: r.controller.SaveSkillResourceHandler},
		Route{Name: "GetSkillResource", Methods: []string{http.MethodGet}, Pattern: "/skills/{name}/resources/{resourcePath:.*}", HandlerFunc: r.controller.GetSkillResourceHandler},
		Route{Name: "DeleteSkillResource", Methods: []string{http.MethodDelete}, Pattern: "/skills/{name}/resources/{resourcePath:.*}", HandlerFunc: r.controller.DeleteSkillResourceHandler},
	}
}
