package controllers_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gorilla/mux"

	"google.golang.org/adk/server/adkrest/controllers"
)

func TestBuilderGetAppHandlerFindsNestedRelativeAgentFile(t *testing.T) {
	appsRoot := t.TempDir()
	tmpRoot := t.TempDir()
	nestedDir := filepath.Join(appsRoot, "novel_pipeline", "chapter_pipeline")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("mkdir nested dir: %v", err)
	}
	want := []byte("name: scene_design_agent\nagent_class: LlmAgent\n")
	if err := os.WriteFile(filepath.Join(nestedDir, "scene_design_agent.yaml"), want, 0o644); err != nil {
		t.Fatalf("write nested yaml: %v", err)
	}

	controller := controllers.NewBuilderAPIController(controllers.BuilderConfig{
		AppsRoot: appsRoot,
		TmpRoot:  tmpRoot,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/builder/app/novel_pipeline?file_path=./scene_design_agent.yaml&tmp=true", nil)
	req = mux.SetURLVars(req, map[string]string{"app": "novel_pipeline"})
	rr := httptest.NewRecorder()

	controller.GetAppHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Body.String(); got != string(want) {
		t.Fatalf("body = %q, want %q", got, string(want))
	}
}
