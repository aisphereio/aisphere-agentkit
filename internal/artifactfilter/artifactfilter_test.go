package artifactfilter

import (
	"reflect"
	"testing"
)

func TestVisibleFileNamesHidesGeneratedBookChapters(t *testing.T) {
	in := []string{
		"user:book_20260602071614_d0c45efe_大清首富__chapter_0001.txt",
		"book_20260602071614_d0c45efe_大清首富__chapter_0258.txt",
		"user:book_20260602071614_d0c45efe_大清首富__manifest.json",
		"user:chapter_analysis_book_1.md",
	}
	want := []string{
		"user:book_20260602071614_d0c45efe_大清首富__manifest.json",
		"user:chapter_analysis_book_1.md",
	}
	if got := VisibleFileNames(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("VisibleFileNames() = %#v, want %#v", got, want)
	}
}
