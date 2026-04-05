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
		{name: "unicode chars", input: "Lösenord", want: "Lösenord"},
		{name: "unicode multi-word", input: "Mina Anteckningar", want: "Mina_Anteckningar"},
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
	if got := TitleFromSlug(""); got != "Untitled" {
		t.Fatalf("TitleFromSlug empty: %q", got)
	}
	if got := MarkdownFilename("Home"); got != "Home.md" {
		t.Fatalf("MarkdownFilename unexpected: %q", got)
	}
	if got := SlugFromFilename("/tmp/My_Page.md"); got != "My_Page" {
		t.Fatalf("SlugFromFilename unexpected: %q", got)
	}
}

func TestDocKindLabel(t *testing.T) {
	if got := KindPage.Label(); got != "page" {
		t.Fatalf("KindPage.Label() = %q, want %q", got, "page")
	}
	if got := KindSkill.Label(); got != "skill" {
		t.Fatalf("KindSkill.Label() = %q, want %q", got, "skill")
	}
}

func TestExtractTags(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "single tag",
			content: "# Title\n\nTags: golang",
			want:    []string{"golang"},
		},
		{
			name:    "multiple tags",
			content: "# Title\n\nTags: go, testing, wiki",
			want:    []string{"go", "testing", "wiki"},
		},
		{
			name:    "tags with extra spaces",
			content: "Tags:   Go ,  Testing ,  Wiki  ",
			want:    []string{"go", "testing", "wiki"},
		},
		{
			name:    "no tags line",
			content: "# Title\n\nJust some content.",
			want:    nil,
		},
		{
			name:    "empty tags value",
			content: "Tags: ",
			want:    nil,
		},
		{
			name:    "case insensitive prefix",
			content: "tags: alpha, beta",
			want:    []string{"alpha", "beta"},
		},
		{
			name:    "tags in middle of content",
			content: "# Skill\n\nSome instructions.\n\nTags: deploy, kubernetes\n\nMore text.",
			want:    []string{"deploy", "kubernetes"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractTags(tt.content)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("expected nil, got %v", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("len mismatch: got %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("tag[%d]: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
