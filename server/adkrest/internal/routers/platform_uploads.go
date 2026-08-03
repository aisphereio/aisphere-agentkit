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

package routers

import "google.golang.org/adk/server/adkrest/controllers"

type PlatformUploadsAPIRouter struct {
	controller *controllers.PlatformUploadsAPIController
}

func NewPlatformUploadsAPIRouter(controller *controllers.PlatformUploadsAPIController) *PlatformUploadsAPIRouter {
	return &PlatformUploadsAPIRouter{controller: controller}
}

func (r *PlatformUploadsAPIRouter) Routes() Routes {
	return Routes{
		{Name: "ListPlatformUploads", Methods: []string{"GET"}, Pattern: "/platform/uploads", HandlerFunc: r.controller.ListUploadsHandler},
		{Name: "CreatePlatformUpload", Methods: []string{"POST"}, Pattern: "/platform/uploads", HandlerFunc: r.controller.CreateUploadHandler},
		{Name: "GetPlatformUpload", Methods: []string{"GET"}, Pattern: "/platform/uploads/{upload_id}", HandlerFunc: r.controller.GetUploadHandler},
		{Name: "PreviewPlatformUpload", Methods: []string{"GET"}, Pattern: "/platform/uploads/{upload_id}/preview", HandlerFunc: r.controller.PreviewUploadHandler},
		{Name: "DownloadPlatformUpload", Methods: []string{"GET"}, Pattern: "/platform/uploads/{upload_id}/content", HandlerFunc: r.controller.DownloadUploadHandler},
		{Name: "DeletePlatformUpload", Methods: []string{"DELETE"}, Pattern: "/platform/uploads/{upload_id}", HandlerFunc: r.controller.DeleteUploadHandler},
		{Name: "AttachPlatformUploadToArtifact", Methods: []string{"POST"}, Pattern: "/platform/uploads/{upload_id}/attach-artifact", HandlerFunc: r.controller.AttachUploadToArtifactHandler},
	}
}
