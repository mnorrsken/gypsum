package wiki

import (
	"strings"
	"testing"
)

func TestRenderUnifiedDiffNoChanges(t *testing.T) {
	html := RenderUnifiedDiff("hello\nworld\n", "hello\nworld\n", "test.md")
	if !strings.Contains(string(html), "No changes") {
		t.Fatal("expected 'No changes' for identical content")
	}
}

func TestRenderUnifiedDiffShowsAddedLine(t *testing.T) {
	old := "line1\nline2\nline3\n"
	new := "line1\nline2\nadded\nline3\n"
	html := RenderUnifiedDiff(old, new, "test.md")
	s := string(html)
	if !strings.Contains(s, "diff-add") {
		t.Fatal("expected added line class")
	}
	if !strings.Contains(s, "+added") {
		t.Fatal("expected '+added' text in diff")
	}
}

func TestRenderUnifiedDiffShowsRemovedLine(t *testing.T) {
	old := "line1\nline2\nline3\n"
	new := "line1\nline3\n"
	html := RenderUnifiedDiff(old, new, "test.md")
	s := string(html)
	if !strings.Contains(s, "diff-del") {
		t.Fatal("expected removed line class")
	}
	if !strings.Contains(s, "-line2") {
		t.Fatal("expected '-line2' text in diff")
	}
}

func TestRenderUnifiedDiffEscapesHTML(t *testing.T) {
	old := "safe\n"
	new := "<script>alert('xss')</script>\n"
	html := RenderUnifiedDiff(old, new, "test.md")
	s := string(html)
	if strings.Contains(s, "<script>") {
		t.Fatal("HTML should be escaped")
	}
}
