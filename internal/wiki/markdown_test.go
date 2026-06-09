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

// TestSecureMacroProcessedInMarkdownContexts verifies that {{secure_aes:...}}
// macros are expanded in all common Markdown formatting contexts.
// These constructs must NOT accidentally suppress macro processing.
func TestSecureMacroProcessedInMarkdownContexts(t *testing.T) {
	r := NewMarkdownRenderer()
	macro := "{{secure_aes:dGVzdA==}}"

	cases := []struct {
		name   string
		source string
	}{
		{"plain paragraph", macro},
		{"surrounded by text", "before " + macro + " after"},
		{"blockquote", "> " + macro},
		{"unordered list item", "- " + macro},
		{"ordered list item", "1. " + macro},
		{"bold", "**" + macro + "**"},
		{"italic", "*" + macro + "*"},
		{"strikethrough", "~~" + macro + "~~"},
		{"h1 heading", "# " + macro},
		{"h2 heading", "## " + macro},
		{"table cell", "| col |\n|-----|\n| " + macro + " |"},
		{"nested blockquote", "> > " + macro},
		{"list then macro", "- item\n\n" + macro},
		{"multiple macros", macro + " and " + macro},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := r.Render(tc.source)
			if err != nil {
				t.Fatalf("Render failed: %v", err)
			}
			if !strings.Contains(string(out), "secure-inline") {
				t.Errorf("macro should be processed in %q context, got:\n%s", tc.name, out)
			}
		})
	}
}

// TestWikiLinkProcessedInMarkdownContexts verifies that [[wiki links]] are
// resolved in all common Markdown formatting contexts.
func TestWikiLinkProcessedInMarkdownContexts(t *testing.T) {
	r := NewMarkdownRenderer()
	link := "[[MyPage]]"

	cases := []struct {
		name   string
		source string
	}{
		{"plain paragraph", link},
		{"surrounded by text", "see " + link + " for details"},
		{"blockquote", "> " + link},
		{"unordered list item", "- " + link},
		{"ordered list item", "1. " + link},
		{"bold", "**" + link + "**"},
		{"italic", "*" + link + "*"},
		{"h2 heading", "## " + link},
		{"table cell", "| col |\n|-----|\n| " + link + " |"},
		{"nested blockquote", "> > " + link},
		{"multiple links", link + " and " + link},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := r.Render(tc.source)
			if err != nil {
				t.Fatalf("Render failed: %v", err)
			}
			if !strings.Contains(string(out), "/wiki/MyPage") {
				t.Errorf("wiki link should be resolved in %q context, got:\n%s", tc.name, out)
			}
		})
	}
}

// TestOnlyBackslashEscapesMacro documents which constructs prevent macro
// processing. Only backslash is a true escape; code spans and fenced blocks
// are intentional code-formatting suppression (issue #25). All other Markdown
// constructs must NOT prevent processing.
func TestOnlyBackslashEscapesMacro(t *testing.T) {
	r := NewMarkdownRenderer()
	macro := "{{secure_aes:dGVzdA==}}"

	suppressed := []struct {
		name   string
		source string
	}{
		{"backslash escape", `\` + macro},
		{"inline code span", "`" + macro + "`"},
		{"triple-backtick code block", "```\n" + macro + "\n```\n"},
		{"tilde code block", "~~~\n" + macro + "\n~~~\n"},
		{"code block with lang tag", "```go\n" + macro + "\n```\n"},
	}
	for _, tc := range suppressed {
		t.Run("suppressed/"+tc.name, func(t *testing.T) {
			out, err := r.Render(tc.source)
			if err != nil {
				t.Fatalf("Render failed: %v", err)
			}
			if strings.Contains(string(out), "secure-inline") {
				t.Errorf("%q should suppress macro processing, but got secure-inline in:\n%s", tc.name, out)
			}
		})
	}

	// Everything else must still process the macro.
	notSuppressed := []struct {
		name   string
		source string
	}{
		{"blockquote", "> " + macro},
		{"list", "- " + macro},
		{"bold", "**" + macro + "**"},
		{"italic", "*" + macro + "*"},
		{"table cell", "| h |\n|---|\n| " + macro + " |"},
		{"definition list term", macro + "\n:   definition"},
		{"footnote text", "text[^1]\n\n[^1]: " + macro},
		{"horizontal rule before", "---\n" + macro},
		{"indented (not code-block)", "  " + macro},
	}
	for _, tc := range notSuppressed {
		t.Run("processed/"+tc.name, func(t *testing.T) {
			out, err := r.Render(tc.source)
			if err != nil {
				t.Fatalf("Render failed: %v", err)
			}
			if !strings.Contains(string(out), "secure-inline") {
				t.Errorf("%q must NOT suppress macro processing, got:\n%s", tc.name, out)
			}
		})
	}
}

// TestOnlyBackslashEscapesWikiLink mirrors TestOnlyBackslashEscapesMacro for
// [[wiki links]].
func TestOnlyBackslashEscapesWikiLink(t *testing.T) {
	r := NewMarkdownRenderer()
	link := "[[MyPage]]"

	suppressed := []struct {
		name   string
		source string
	}{
		{"backslash escape", `\` + link},
		{"inline code span", "`" + link + "`"},
		{"triple-backtick code block", "```\n" + link + "\n```\n"},
		{"tilde code block", "~~~\n" + link + "\n~~~\n"},
	}
	for _, tc := range suppressed {
		t.Run("suppressed/"+tc.name, func(t *testing.T) {
			out, err := r.Render(tc.source)
			if err != nil {
				t.Fatalf("Render failed: %v", err)
			}
			if strings.Contains(string(out), "/wiki/MyPage") {
				t.Errorf("%q should suppress wiki link, but got /wiki/ in:\n%s", tc.name, out)
			}
		})
	}

	notSuppressed := []struct {
		name   string
		source string
	}{
		{"blockquote", "> " + link},
		{"list", "- " + link},
		{"bold", "**" + link + "**"},
		{"italic", "*" + link + "*"},
		{"table cell", "| h |\n|---|\n| " + link + " |"},
		{"horizontal rule before", "---\n" + link},
		{"indented (not code-block)", "  " + link},
	}
	for _, tc := range notSuppressed {
		t.Run("processed/"+tc.name, func(t *testing.T) {
			out, err := r.Render(tc.source)
			if err != nil {
				t.Fatalf("Render failed: %v", err)
			}
			if !strings.Contains(string(out), "/wiki/MyPage") {
				t.Errorf("%q must NOT suppress wiki link, got:\n%s", tc.name, out)
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

func TestCodeSpanSuppressesWikiLink(t *testing.T) {
	r := NewMarkdownRenderer()
	out, err := r.Render("Use `[[Page]]` for something.")
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	html := string(out)
	if strings.Contains(html, "/wiki/") {
		t.Errorf("wiki link inside code span should not be resolved, got: %s", html)
	}
	if !strings.Contains(html, "[[Page]]") {
		t.Errorf("expected literal [[Page]] in code output, got: %s", html)
	}
}

func TestCodeSpanSuppressesSecureMacro(t *testing.T) {
	r := NewMarkdownRenderer()
	out, err := r.Render("Example: `{{secure_aes:dGVzdA==}}`")
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	html := string(out)
	if strings.Contains(html, "secure-inline") {
		t.Errorf("secure macro inside code span should not be expanded, got: %s", html)
	}
	if !strings.Contains(html, "{{secure_aes:dGVzdA==}}") {
		t.Errorf("expected literal macro text in code output, got: %s", html)
	}
}

func TestFencedBlockSuppressesWikiLink(t *testing.T) {
	r := NewMarkdownRenderer()
	out, err := r.Render("```\n[[Page]]\n```\n")
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	html := string(out)
	if strings.Contains(html, "/wiki/") {
		t.Errorf("wiki link inside fenced code block should not be resolved, got: %s", html)
	}
}

func TestFencedBlockSuppressesSecureMacro(t *testing.T) {
	r := NewMarkdownRenderer()
	out, err := r.Render("```\n{{secure_aes:dGVzdA==}}\n```\n")
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	html := string(out)
	if strings.Contains(html, "secure-inline") {
		t.Errorf("secure macro inside fenced code block should not be expanded, got: %s", html)
	}
}

func TestBackslashEscapedSecureMacro(t *testing.T) {
	r := NewMarkdownRenderer()
	out, err := r.Render(`\{{secure_aes:dGVzdA==}} is literal`)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	html := string(out)
	if strings.Contains(html, "secure-inline") {
		t.Errorf("backslash-escaped secure macro should not be expanded, got: %s", html)
	}
	if !strings.Contains(html, "{{secure_aes:dGVzdA==}}") {
		t.Errorf("expected literal macro text, got: %s", html)
	}
}

func TestBackslashEscapedWikiLink(t *testing.T) {
	r := NewMarkdownRenderer()
	out, err := r.Render(`\[[Page]] is not a link`)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	html := string(out)
	if strings.Contains(html, "/wiki/") {
		t.Errorf("backslash-escaped wiki link should not be resolved, got: %s", html)
	}
	if !strings.Contains(html, "[[Page]]") {
		t.Errorf("expected literal [[Page]] text, got: %s", html)
	}
}

func TestBackslashEscapesStandardMarkdown(t *testing.T) {
	r := NewMarkdownRenderer()
	// Goldmark (CommonMark) natively handles \* to suppress bold/italic.
	out, err := r.Render(`\*not bold\*`)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	html := string(out)
	if strings.Contains(html, "<strong>") || strings.Contains(html, "<em>") {
		t.Errorf("backslash-escaped asterisks should not produce bold/italic, got: %s", html)
	}
	if !strings.Contains(html, "*not bold*") {
		t.Errorf("expected literal asterisks, got: %s", html)
	}
}

// TestBackslashEscapeInCodeSpan verifies that \[[Page]] and \{{secure_aes:...}}
// inside backtick code spans render without the leading backslash.
func TestBackslashEscapeInCodeSpan(t *testing.T) {
	r := NewMarkdownRenderer()

	t.Run("wiki link escape in code span", func(t *testing.T) {
		out, err := r.Render("`\\[[Page]]`")
		if err != nil {
			t.Fatalf("Render failed: %v", err)
		}
		html := string(out)
		if strings.Contains(html, `\[[`) {
			t.Errorf("backslash should be stripped inside code span, got: %s", html)
		}
		if !strings.Contains(html, "[[Page]]") {
			t.Errorf("expected [[Page]] without backslash, got: %s", html)
		}
	})

	t.Run("secure macro escape in code span", func(t *testing.T) {
		out, err := r.Render("`\\{{secure_aes:dGVzdA==}}`")
		if err != nil {
			t.Fatalf("Render failed: %v", err)
		}
		html := string(out)
		if strings.Contains(html, `\{{`) {
			t.Errorf("backslash should be stripped inside code span, got: %s", html)
		}
		if !strings.Contains(html, "{{secure_aes:dGVzdA==}}") {
			t.Errorf("expected literal macro text without backslash, got: %s", html)
		}
		if strings.Contains(html, "secure-inline") {
			t.Errorf("macro must not be expanded inside code span, got: %s", html)
		}
	})

	t.Run("wiki link escape in fenced block", func(t *testing.T) {
		out, err := r.Render("```\n\\[[Page]]\n```\n")
		if err != nil {
			t.Fatalf("Render failed: %v", err)
		}
		html := string(out)
		if strings.Contains(html, `\[[`) {
			t.Errorf("backslash should be stripped inside fenced block, got: %s", html)
		}
		if !strings.Contains(html, "[[Page]]") {
			t.Errorf("expected [[Page]] without backslash, got: %s", html)
		}
	})

	t.Run("secure macro escape in fenced block", func(t *testing.T) {
		out, err := r.Render("```\n\\{{secure_aes:dGVzdA==}}\n```\n")
		if err != nil {
			t.Fatalf("Render failed: %v", err)
		}
		html := string(out)
		if strings.Contains(html, `\{{`) {
			t.Errorf("backslash should be stripped inside fenced block, got: %s", html)
		}
		if strings.Contains(html, "secure-inline") {
			t.Errorf("macro must not be expanded inside fenced block, got: %s", html)
		}
	})
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

// TestRenderPublicSanitizesHTML verifies that public (anonymous) rendering
// strips author-supplied HTML/JS while authenticated rendering keeps it, and
// that legitimate goldmark output survives the sanitizer.
func TestRenderPublicSanitizesHTML(t *testing.T) {
	r := NewMarkdownRenderer()

	render := func(t *testing.T, src string) string {
		t.Helper()
		out, err := r.RenderPublic(src)
		if err != nil {
			t.Fatalf("RenderPublic failed: %v", err)
		}
		return string(out)
	}

	t.Run("raw script is stripped on public render only", func(t *testing.T) {
		src := "hello\n\n<script>alert(1)</script>\n\nworld"
		html := render(t, src)
		if strings.Contains(html, "<script>") {
			t.Errorf("public render must not contain script tags, got: %s", html)
		}
		if !strings.Contains(html, "hello") || !strings.Contains(html, "world") {
			t.Errorf("surrounding text should be preserved, got: %s", html)
		}
		priv, err := r.Render(src)
		if err != nil {
			t.Fatalf("Render failed: %v", err)
		}
		if !strings.Contains(string(priv), "<script>") {
			t.Errorf("authenticated render should keep raw HTML, got: %s", priv)
		}
	})

	t.Run("event handler attributes are stripped", func(t *testing.T) {
		html := render(t, `<img src="/images/a.png" onerror="alert(1)">`)
		if strings.Contains(html, "onerror") {
			t.Errorf("public render must not contain event handlers, got: %s", html)
		}
	})

	t.Run("javascript urls do not reach public output", func(t *testing.T) {
		html := render(t, "![x|500](javascript:alert(1))\n\n[link](javascript:alert(2))")
		if strings.Contains(html, "javascript:") {
			t.Errorf("public render must not contain javascript URLs, got: %s", html)
		}
	})

	t.Run("sized images survive sanitization", func(t *testing.T) {
		html := render(t, "![photo|500x300](/images/test.png)")
		if !strings.Contains(html, "<img") {
			t.Errorf("expected <img> tag, got: %s", html)
		}
		if !strings.Contains(html, "width: 500px") && !strings.Contains(html, "width:500px") {
			t.Errorf("expected size style to survive, got: %s", html)
		}
	})

	t.Run("task list checkboxes survive sanitization", func(t *testing.T) {
		html := render(t, "- [x] done\n- [ ] todo")
		if !strings.Contains(html, `type="checkbox"`) {
			t.Errorf("expected task-list checkbox to survive, got: %s", html)
		}
	})

	t.Run("code highlighting markup survives sanitization", func(t *testing.T) {
		html := render(t, "```go\nfunc main() {}\n```")
		if !strings.Contains(html, "<pre") || !strings.Contains(html, "<code") {
			t.Errorf("expected pre/code blocks, got: %s", html)
		}
		if !strings.Contains(html, "class=") {
			t.Errorf("expected chroma highlight classes to survive, got: %s", html)
		}
	})
}

// TestExpandImageSizeMacroEscaping verifies that the raw <img> emitted by the
// image-size macro cannot be broken out of via alt text or the URL.
func TestExpandImageSizeMacroEscaping(t *testing.T) {
	r := NewMarkdownRenderer()

	t.Run("quotes in alt cannot break out of the attribute", func(t *testing.T) {
		out, err := r.Render(`![x" onerror=alert(1)|500](/images/a.png)`)
		if err != nil {
			t.Fatalf("Render failed: %v", err)
		}
		html := string(out)
		if strings.Contains(html, `alt="x" `) {
			t.Errorf("quote broke out of alt attribute: %s", html)
		}
		if !strings.Contains(html, "&#34;") {
			t.Errorf("expected escaped quote in alt, got: %s", html)
		}
	})

	t.Run("javascript url leaves macro unexpanded", func(t *testing.T) {
		out, err := r.Render("![x|500](javascript:alert(1))")
		if err != nil {
			t.Fatalf("Render failed: %v", err)
		}
		if strings.Contains(string(out), "style=") {
			t.Errorf("macro should not expand for javascript URL: %s", out)
		}
	})
}
