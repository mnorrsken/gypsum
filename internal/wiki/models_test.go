package wiki

import "testing"

func TestSlugFromTitle(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "basic words", input: "Hello World", want: "Hello_World"},
		{name: "trims spaces", input: "  Project Notes  ", want: "Project_Notes"},
		{name: "drops special chars", input: "A/B:C*D?", want: "ABCD"},
		{name: "empty falls back", input: "   ", want: "Untitled"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := SlugFromTitle(test.input)
			if got != test.want {
				t.Fatalf("SlugFromTitle(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestTitleAndFilenameHelpers(t *testing.T) {
	if got := TitleFromSlug("Architecture_Notes"); got != "Architecture Notes" {
		t.Fatalf("TitleFromSlug unexpected: %q", got)
	}
	if got := MarkdownFilename("Home"); got != "Home.md" {
		t.Fatalf("MarkdownFilename unexpected: %q", got)
	}
	if got := SlugFromFilename("/tmp/My_Page.md"); got != "My_Page" {
		t.Fatalf("SlugFromFilename unexpected: %q", got)
	}
}
