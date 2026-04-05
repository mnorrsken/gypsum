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

func TestRenderPublic(t *testing.T) {
	r := NewMarkdownRenderer()

	t.Run("wiki links become plain text", func(t *testing.T) {
		out, err := r.RenderPublic("See [[My Page]] for details.")
		if err != nil {
			t.Fatalf("RenderPublic failed: %v", err)
		}
		html := string(out)
		if strings.Contains(html, "<a ") {
			t.Errorf("public render should not contain links for wiki links, got: %s", html)
		}
		if !strings.Contains(html, "My Page") {
			t.Errorf("expected plain text 'My Page', got: %s", html)
		}
	})

	t.Run("secure macros are stripped", func(t *testing.T) {
		out, err := r.RenderPublic("Hello {{secure_aes:abc123def}} world")
		if err != nil {
			t.Fatalf("RenderPublic failed: %v", err)
		}
		html := string(out)
		if strings.Contains(html, "secure_aes") {
			t.Errorf("public render should strip secure macros, got: %s", html)
		}
		if strings.Contains(html, "abc123def") {
			t.Errorf("public render should not contain ciphertext, got: %s", html)
		}
		if !strings.Contains(html, "Hello") || !strings.Contains(html, "world") {
			t.Errorf("surrounding text should be preserved, got: %s", html)
		}
	})

	t.Run("basic markdown renders", func(t *testing.T) {
		out, err := r.RenderPublic("**bold** and *italic*")
		if err != nil {
			t.Fatalf("RenderPublic failed: %v", err)
		}
		html := string(out)
		if !strings.Contains(html, "<strong>bold</strong>") {
			t.Errorf("expected bold HTML, got: %s", html)
		}
		if !strings.Contains(html, "<em>italic</em>") {
			t.Errorf("expected italic HTML, got: %s", html)
		}
	})
}

func TestExpandImageSizeMacros(t *testing.T) {
	r := NewMarkdownRenderer()

	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{
			name:     "pixel width",
			input:    "![photo|500](/images/test.png)",
			contains: `max-width:500px`,
		},
		{
			name:     "percentage width",
			input:    "![photo|50%](/images/test.png)",
			contains: `max-width:50%`,
		},
		{
			name:     "explicit dimensions",
			input:    "![photo|800x600](/images/test.png)",
			contains: `width:800px;height:600px`,
		},
		{
			name:     "alt text preserved",
			input:    "![my alt text|300](/images/test.png)",
			contains: `alt="my alt text"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := r.Render(tt.input)
			if err != nil {
				t.Fatalf("Render failed: %v", err)
			}
			html := string(out)
			if !strings.Contains(html, tt.contains) {
				t.Errorf("expected %q in output, got: %s", tt.contains, html)
			}
			if !strings.Contains(html, "<img ") {
				t.Errorf("expected <img> tag, got: %s", html)
			}
		})
	}
}

func TestRenderSecurePlaceholders(t *testing.T) {
	r := NewMarkdownRenderer()
	out, err := r.Render("Hello {{secure_aes:dGVzdA==}} world")
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	html := string(out)
	if !strings.Contains(html, "secure-inline") {
		t.Errorf("expected secure-inline span, got: %s", html)
	}
	if !strings.Contains(html, "secure-copy-btn") {
		t.Errorf("expected secure-copy-btn button, got: %s", html)
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
