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

type PlatformNovelsAPIRouter struct {
	controller *controllers.PlatformNovelsAPIController
}

func NewPlatformNovelsAPIRouter(controller *controllers.PlatformNovelsAPIController) *PlatformNovelsAPIRouter {
	return &PlatformNovelsAPIRouter{controller: controller}
}

func (r *PlatformNovelsAPIRouter) Routes() Routes {
	return Routes{
		{Name: "ListProjectNovels", Methods: []string{"GET"}, Pattern: "/platform/projects/{project_id}/novels", HandlerFunc: r.controller.ListBooksHandler},
		{Name: "ImportProjectNovelFile", Methods: []string{"POST"}, Pattern: "/platform/projects/{project_id}/novels/import-file", HandlerFunc: r.controller.ImportFileHandler},
		{Name: "ImportProjectNovelUpload", Methods: []string{"POST"}, Pattern: "/platform/projects/{project_id}/novels/import-upload", HandlerFunc: r.controller.ImportUploadHandler},
		{Name: "GetProjectNovel", Methods: []string{"GET"}, Pattern: "/platform/projects/{project_id}/novels/{book_id}", HandlerFunc: r.controller.GetBookHandler},
		{Name: "PreviewProjectNovelSplit", Methods: []string{"POST"}, Pattern: "/platform/projects/{project_id}/novels/{book_id}/split-preview", HandlerFunc: r.controller.SplitPreviewHandler},
		{Name: "CommitProjectNovelSplit", Methods: []string{"POST"}, Pattern: "/platform/projects/{project_id}/novels/{book_id}/split-commit", HandlerFunc: r.controller.SplitCommitHandler},
		{Name: "ListProjectNovelChapters", Methods: []string{"GET"}, Pattern: "/platform/projects/{project_id}/novels/{book_id}/chapters", HandlerFunc: r.controller.ListChaptersHandler},
		{Name: "GetProjectNovelChapter", Methods: []string{"GET"}, Pattern: "/platform/projects/{project_id}/novels/{book_id}/chapters/{chapter_no}", HandlerFunc: r.controller.GetChapterHandler},
	}
}
