package artifactfilter

import (
	"regexp"
	"strings"
)

var generatedBookChapterRE = regexp.MustCompile(`^(?:user:)?book_[^/\\]+__chapter_[0-9]{4,}\.txt$`)

// VisibleFileNames returns artifact names suitable for model-facing lists.
// Generated split chapter files can be numerous and are derived assets; agents
// should reach them through book_get_chapter/novel_get_chapter instead of
// reading raw artifact names directly.
func VisibleFileNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if IsGeneratedBookChapter(name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

func IsGeneratedBookChapter(name string) bool {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	if name == "" {
		return false
	}
	return generatedBookChapterRE.MatchString(name)
}
