package wiki

import (
	"strings"
	"testing"
)

func TestExtractH1Title(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		wantTitle string
		wantRest  string
	}{
		{
			name:      "simple H1",
			source:    "# Hello World\n\nSome content.",
			wantTitle: "Hello World",
			wantRest:  "\nSome content.",
		},
		{
			name:      "H1 with special characters",
			source:    "# Tokens & Lösenord\n\nContent here.",
			wantTitle: "Tokens & Lösenord",
			wantRest:  "\nContent here.",
		},
		{
			name:      "no H1 heading",
			source:    "## Subheading\n\nContent.",
			wantTitle: "",
			wantRest:  "## Subheading\n\nContent.",
		},
		{
			name:      "H1 only",
			source:    "# Just a Title",
			wantTitle: "Just a Title",
			wantRest:  "",
		},
		{
			name:      "leading newlines then H1",
			source:    "\n# Title Here\n\nBody.",
			wantTitle: "Title Here",
			wantRest:  "\nBody.",
		},
		{
			name:      "H2 is not H1",
			source:    "## Not H1\n\nContent.",
			wantTitle: "",
			wantRest:  "## Not H1\n\nContent.",
		},
		{
			name:      "H1 without space after hash is not H1",
			source:    "#NotH1\n\nContent.",
			wantTitle: "",
			wantRest:  "#NotH1\n\nContent.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTitle, gotRest := ExtractH1Title(tt.source)
			if gotTitle != tt.wantTitle {
				t.Errorf("title: got %q, want %q", gotTitle, tt.wantTitle)
			}
			if gotRest != tt.wantRest {
				t.Errorf("rest: got %q, want %q", gotRest, tt.wantRest)
			}
		})
	}
}

func TestWikiLinkIncludesTitleParam(t *testing.T) {
	r := NewMarkdownRenderer()
	out, err := r.Render("[[ Tokens & Lösenord ]]")
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	html := string(out)
	// Slug for "Tokens & Lösenord": spaces→underscores gives "Tokens_&_Lösenord",
	// then & is stripped, leaving "Tokens__Lösenord" (double underscore).
	if !strings.Contains(html, "/wiki/Tokens__L") {
		t.Errorf("expected slug URL in output, got: %s", html)
	}
	// The rendered link should carry a title= query param
	if !strings.Contains(html, "title=") {
		t.Errorf("expected title= query param in wiki link, got: %s", html)
	}
	// The link text should use the original (trimmed) title
	if !strings.Contains(html, "Tokens &amp; Lösenord") {
		t.Errorf("expected original title as link text, got: %s", html)
	}
}
