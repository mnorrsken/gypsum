package wiki

import "testing"

func TestConvertMediaWikiToMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "headings",
			input:    "= H1 =\n== H2 ==\n=== H3 ===\n==== H4 ====\n===== H5 =====\n====== H6 ======",
			expected: "# H1\n## H2\n### H3\n#### H4\n##### H5\n###### H6\n",
		},
		{
			name:     "bold",
			input:    "This is '''bold''' text.",
			expected: "This is **bold** text.\n",
		},
		{
			name:     "italic",
			input:    "This is ''italic'' text.",
			expected: "This is *italic* text.\n",
		},
		{
			name:     "bold italic",
			input:    "'''''bold italic'''''",
			expected: "***bold italic***\n",
		},
		{
			name:  "syntaxhighlight with lang",
			input: "<syntaxhighlight lang=\"python\">\nprint(\"hello\")\n</syntaxhighlight>",
			expected: "```python\nprint(\"hello\")\n```\n",
		},
		{
			name:  "syntaxhighlight without lang",
			input: "<syntaxhighlight>\nsome code\n</syntaxhighlight>",
			expected: "```\nsome code\n```\n",
		},
		{
			name:  "source tag",
			input: "<source lang=\"bash\">\necho hello\n</source>",
			expected: "```bash\necho hello\n```\n",
		},
		{
			name:     "pre tag",
			input:    "<pre>\npreformatted text\n</pre>",
			expected: "```\npreformatted text\n```\n",
		},
		{
			name:     "nowiki inline",
			input:    "show <nowiki>[[not a link]]</nowiki> literally",
			expected: "show \\[\\[not a link\\]\\] literally\n",
		},
		{
			name:     "nowiki with leading space becomes code block",
			input:    " <nowiki>x = 1\ny = 2</nowiki>",
			expected: "```\nx = 1\ny = 2\n```\n",
		},
		{
			name:     "space-prefixed lines become code block",
			input:    "normal text\n code line 1\n code line 2\nback to normal",
			expected: "normal text\n```\ncode line 1\ncode line 2\n```\nback to normal\n",
		},
		{
			name:     "single space-prefixed line",
			input:    " just one line of code",
			expected: "```\njust one line of code\n```\n",
		},
		{
			name:     "code tag",
			input:    "use <code>git status</code> to check",
			expected: "use `git status` to check\n",
		},
		{
			name:     "unordered list",
			input:    "* Item 1\n** Sub-item\n* Item 2",
			expected: "- Item 1\n  - Sub-item\n- Item 2\n",
		},
		{
			name:     "ordered list",
			input:    "# First\n# Second\n## Sub-item",
			expected: "1. First\n1. Second\n  1. Sub-item\n",
		},
		{
			name:     "definition list",
			input:    ";Term\n:Definition",
			expected: "**Term**\n> Definition\n",
		},
		{
			name:     "external link with text",
			input:    "[https://example.com Example Site]",
			expected: "[Example Site](https://example.com)\n",
		},
		{
			name:     "bare external link",
			input:    "[https://example.com]",
			expected: "https://example.com\n",
		},
		{
			name:     "wiki link plain",
			input:    "See [[Some Page]] for details.",
			expected: "See [[Some Page]] for details.\n",
		},
		{
			name:     "wiki link with display text",
			input:    "See [[Some Page|click here]] for details.",
			expected: "See [[Some Page]] for details.\n",
		},
		{
			name:     "wiki link with section",
			input:    "See [[Some Page#Section]] for details.",
			expected: "See [[Some Page]] for details.\n",
		},
		{
			name:     "wiki link with section and display",
			input:    "See [[Some Page#Section|click here]] for details.",
			expected: "See [[Some Page]] for details.\n",
		},
		{
			name:     "horizontal rule",
			input:    "above\n----\nbelow",
			expected: "above\n---\nbelow\n",
		},
		{
			name:     "remove categories",
			input:    "Some text\n[[Category:Foo]]\n[[Category:Bar]]",
			expected: "Some text\n",
		},
		{
			name:     "remove templates",
			input:    "Before {{Infobox|name=foo}} after",
			expected: "Before  after\n",
		},
		{
			name:     "remove refs",
			input:    "Fact<ref>Source 2020</ref> here.",
			expected: "Fact here.\n",
		},
		{
			name:     "remove magic words",
			input:    "__TOC__\nSome text\n__NOTOC__",
			expected: "Some text\n",
		},
		{
			name:     "file link",
			input:    "[[File:diagram.png|thumb|A diagram]]",
			expected: "![diagram.png](diagram.png)\n",
		},
		{
			name:     "br tags",
			input:    "line one<br>line two<br />line three",
			expected: "line one\nline two\nline three\n",
		},
		{
			name:     "html entities",
			input:    "A &amp; B &lt; C",
			expected: "A & B < C\n",
		},
		{
			name: "simple table",
			input: `{| class="wikitable"
|-
! Name !! Age
|-
| Alice || 30
|-
| Bob || 25
|}`,
			expected: "| Name | Age |\n| --- | --- |\n| Alice | 30 |\n| Bob | 25 |\n",
		},
		{
			name: "table with cell attributes",
			input: `{| class="wikitable"
|-
! Header
|-
| style="color:red" | Red text
|}`,
			expected: "| Header |\n| --- |\n| Red text |\n",
		},
		{
			name: "table without explicit headers",
			input: `{|
|-
| A || B
|-
| C || D
|}`,
			expected: "| A | B |\n| --- | --- |\n| C | D |\n",
		},
		{
			name: "comprehensive example",
			input: `= My Page =

== Introduction ==

This is '''bold''' and ''italic'' text with a [[Wiki Link|link]].

* First item
* Second item
** Sub-item

<syntaxhighlight lang="go">
func main() {
    fmt.Println("hello")
}
</syntaxhighlight>

See [https://golang.org Go website] for more.

{| class="wikitable"
|-
! Language !! Year
|-
| Go || 2009
|-
| Rust || 2010
|}

[[Category:Programming]]`,
			expected: `# My Page

## Introduction

This is **bold** and *italic* text with a [[Wiki Link]].

- First item
- Second item
  - Sub-item

` + "```go" + `
func main() {
    fmt.Println("hello")
}
` + "```" + `

See [Go website](https://golang.org) for more.

| Language | Year |
| --- | --- |
| Go | 2009 |
| Rust | 2010 |
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConvertMediaWikiToMarkdown(tt.input)
			if got != tt.expected {
				t.Errorf("ConvertMediaWikiToMarkdown(%q)\ngot:\n%s\nwant:\n%s", tt.input, got, tt.expected)
			}
		})
	}
}

func TestStripTableCellAttrs(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`style="color:red" | content`, "content"},
		{`class="foo" align="center" | text`, "text"},
		{"plain text", "plain text"},
		{"no attrs | with pipe", "no attrs | with pipe"}, // no = before |, keep as-is
	}

	for _, tt := range tests {
		got := stripTableCellAttrs(tt.input)
		if got != tt.expected {
			t.Errorf("stripTableCellAttrs(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestEscapeMarkdownChars(t *testing.T) {
	input := `*bold* _under_ [link] {brace} #heading | \back`
	expected := `\*bold\* \_under\_ \[link\] \{brace\} \#heading \| \\back`
	got := escapeMarkdownChars(input)
	if got != expected {
		t.Errorf("escapeMarkdownChars(%q) = %q, want %q", input, got, expected)
	}
}
