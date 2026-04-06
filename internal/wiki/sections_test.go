package wiki

import (
	"strings"
	"testing"
)

func TestParseSections_NoHeadings(t *testing.T) {
	content := "Just some text\nwith no headings.\n"
	sections := ParseSections(content)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	if sections[0].Heading != "" {
		t.Errorf("expected empty heading for intro, got %q", sections[0].Heading)
	}
	if sections[0].Body != content {
		t.Errorf("body mismatch: %q", sections[0].Body)
	}
}

func TestParseSections_OnlyH1Headings(t *testing.T) {
	content := "# Alpha\nContent A\n# Beta\nContent B\n"
	sections := ParseSections(content)
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
	if sections[0].Heading != "Alpha" || sections[0].Level != 1 {
		t.Errorf("unexpected section 0: heading=%q level=%d", sections[0].Heading, sections[0].Level)
	}
	if sections[1].Heading != "Beta" || sections[1].Level != 1 {
		t.Errorf("unexpected section 1: heading=%q level=%d", sections[1].Heading, sections[1].Level)
	}
}

func TestParseSections_IntroAndHeadings(t *testing.T) {
	content := "Intro text\n\n# First\nBody 1\n# Second\nBody 2\n"
	sections := ParseSections(content)
	if len(sections) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(sections))
	}
	if sections[0].Heading != "" {
		t.Errorf("expected intro section, got heading %q", sections[0].Heading)
	}
	if sections[1].Heading != "First" {
		t.Errorf("expected First, got %q", sections[1].Heading)
	}
	if sections[2].Heading != "Second" {
		t.Errorf("expected Second, got %q", sections[2].Heading)
	}
}

func TestParseSections_H2Splits(t *testing.T) {
	content := "# Title\nIntro\n## Subsection\nSub content\n"
	sections := ParseSections(content)
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections (## should split), got %d", len(sections))
	}
	if sections[0].Heading != "Title" || sections[0].Level != 1 {
		t.Errorf("unexpected section 0: %+v", sections[0])
	}
	if sections[1].Heading != "Subsection" || sections[1].Level != 2 {
		t.Errorf("unexpected section 1: %+v", sections[1])
	}
}

func TestParseSections_MixedLevels(t *testing.T) {
	content := "# Page Title\n\n## Intro\nText\n\n### Detail\nMore\n\n## Summary\nEnd\n"
	sections := ParseSections(content)
	if len(sections) != 4 {
		t.Fatalf("expected 4 sections, got %d", len(sections))
	}
	cases := []struct{ heading string; level int }{
		{"Page Title", 1},
		{"Intro", 2},
		{"Detail", 3},
		{"Summary", 2},
	}
	for i, c := range cases {
		if sections[i].Heading != c.heading || sections[i].Level != c.level {
			t.Errorf("section %d: got heading=%q level=%d, want heading=%q level=%d",
				i, sections[i].Heading, sections[i].Level, c.heading, c.level)
		}
	}
}

func TestParseSections_EmptyContent(t *testing.T) {
	sections := ParseSections("")
	if len(sections) != 0 {
		t.Fatalf("expected 0 sections for empty content, got %d", len(sections))
	}
}

func TestParseSections_ConsecutiveHeadings(t *testing.T) {
	content := "# A\n# B\n# C\n"
	sections := ParseSections(content)
	if len(sections) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(sections))
	}
	for i, want := range []string{"A", "B", "C"} {
		if sections[i].Heading != want {
			t.Errorf("section %d: expected %q, got %q", i, want, sections[i].Heading)
		}
	}
}

func TestListSectionHeadings(t *testing.T) {
	content := "Intro\n# Alpha\nBody\n## Beta\nBody\n"
	headings := ListSectionHeadings(content)
	if len(headings) != 2 {
		t.Fatalf("expected 2 headings, got %d", len(headings))
	}
	if headings[0] != "# Alpha" || headings[1] != "## Beta" {
		t.Errorf("unexpected headings: %v", headings)
	}
}

func TestListSectionHeadings_None(t *testing.T) {
	headings := ListSectionHeadings("No headings here\n")
	if len(headings) != 0 {
		t.Fatalf("expected 0 headings, got %d", len(headings))
	}
}

func TestGetSection_ExactMatch(t *testing.T) {
	content := "# Title\nPage title body\n## History\nSome history\n## Notes\nSome notes\n"
	body, err := GetSection(content, "History")
	if err != nil {
		t.Fatal(err)
	}
	if body != "## History\nSome history\n" {
		t.Errorf("unexpected body: %q", body)
	}
}

func TestGetSection_CaseInsensitive(t *testing.T) {
	content := "## History\nSome history\n"
	body, err := GetSection(content, "history")
	if err != nil {
		t.Fatal(err)
	}
	if body != "## History\nSome history\n" {
		t.Errorf("unexpected body: %q", body)
	}
}

func TestGetSection_WithHashPrefix(t *testing.T) {
	content := "## Introduction\nText\n## Summary\nEnd\n"
	// Caller passes "## Introduction" — leading ## should be stripped.
	body, err := GetSection(content, "## Introduction")
	if err != nil {
		t.Fatal(err)
	}
	if body != "## Introduction\nText\n" {
		t.Errorf("unexpected body: %q", body)
	}
}

func TestGetSection_NotFound(t *testing.T) {
	content := "# Alpha\nBody\n## Beta\nBody\n"
	_, err := GetSection(content, "Gamma")
	if err == nil {
		t.Fatal("expected error for missing section")
	}
	if got := err.Error(); got != "section not found: 'Gamma'. Available sections: # Alpha, ## Beta" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestGetSection_NoHeadings(t *testing.T) {
	_, err := GetSection("plain text\n", "Anything")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "section not found: 'Anything' (page has no headings)" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestReplaceSection_H1Basic(t *testing.T) {
	content := "# Title\nOld title body\n# History\nOld history\n# Notes\nOld notes\n"
	result, err := ReplaceSection(content, "History", "New history content\nLine 2")
	if err != nil {
		t.Fatal(err)
	}
	want := "# Title\nOld title body\n# History\nNew history content\nLine 2\n# Notes\nOld notes\n"
	if result != want {
		t.Errorf("got:\n%s\nwant:\n%s", result, want)
	}
}

func TestReplaceSection_H2PreservesLevel(t *testing.T) {
	content := "# Title\nIntro\n## Section A\nOld A\n## Section B\nOld B\n"
	result, err := ReplaceSection(content, "Section A", "New A content")
	if err != nil {
		t.Fatal(err)
	}
	want := "# Title\nIntro\n## Section A\nNew A content\n## Section B\nOld B\n"
	if result != want {
		t.Errorf("got:\n%s\nwant:\n%s", result, want)
	}
}

func TestReplaceSection_WithHashPrefix(t *testing.T) {
	content := "## Introduction\nOld\n## Summary\nEnd\n"
	result, err := ReplaceSection(content, "## Introduction", "New intro")
	if err != nil {
		t.Fatal(err)
	}
	want := "## Introduction\nNew intro\n## Summary\nEnd\n"
	if result != want {
		t.Errorf("got: %q, want: %q", result, want)
	}
}

func TestReplaceSection_CaseInsensitive(t *testing.T) {
	content := "# History\nOld\n"
	result, err := ReplaceSection(content, "HISTORY", "New")
	if err != nil {
		t.Fatal(err)
	}
	want := "# History\nNew\n"
	if result != want {
		t.Errorf("got: %q, want: %q", result, want)
	}
}

func TestReplaceSection_NotFound(t *testing.T) {
	content := "# Alpha\nBody\n"
	_, err := ReplaceSection(content, "Beta", "new")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetSection_Ambiguous(t *testing.T) {
	content := "# Project1\n## Budget\nOld\n## Plan\nPlan1\n# Project2\n## Budget\nOld\n## Plan\nPlan2\n"
	_, err := GetSection(content, "Budget")
	if err == nil {
		t.Fatal("expected ambiguous error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected 'ambiguous' in error, got: %s", err)
	}
}

func TestReplaceSection_Ambiguous(t *testing.T) {
	content := "# Project1\n## Budget\nOld\n## Plan\nPlan1\n# Project2\n## Budget\nOld\n## Plan\nPlan2\n"
	_, err := ReplaceSection(content, "Budget", "New budget")
	if err == nil {
		t.Fatal("expected ambiguous error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected 'ambiguous' in error, got: %s", err)
	}
}

func TestReplaceSection_EmptyNewBody(t *testing.T) {
	content := "# Title\nBody\n## ToEmpty\nOld content\n## After\nKeep\n"
	result, err := ReplaceSection(content, "ToEmpty", "")
	if err != nil {
		t.Fatal(err)
	}
	want := "# Title\nBody\n## ToEmpty\n## After\nKeep\n"
	if result != want {
		t.Errorf("got:\n%s\nwant:\n%s", result, want)
	}
}

func TestReplaceSection_PreservesIntro(t *testing.T) {
	content := "Intro text\n\n## Section\nOld body\n"
	result, err := ReplaceSection(content, "Section", "New body")
	if err != nil {
		t.Fatal(err)
	}
	want := "Intro text\n\n## Section\nNew body\n"
	if result != want {
		t.Errorf("got:\n%s\nwant:\n%s", result, want)
	}
}
