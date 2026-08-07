package configurable

// retiredAISphereToolReferences are historical global configurable entries that
// conflict with the accepted AISphere Runtime architecture. They are removed
// after the upstream-compatible registry is initialized so neither local YAML
// agents nor the production compatibility resolver can accidentally revive the
// rejected execution model.
var retiredAISphereToolReferences = []string{
	"PlanRunToolset",
	"files_retrieval",
	"FilesRetrieval",
}

func init() {
	registryMu.Lock()
	defer registryMu.Unlock()
	for _, name := range retiredAISphereToolReferences {
		delete(toolRegistry, name)
	}
}
