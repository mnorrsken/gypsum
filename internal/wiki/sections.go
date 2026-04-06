package wiki

import (
	"fmt"
	"strings"
)

// Section represents a portion of markdown content delimited by headings.
type Section struct {
	Heading string // heading text (without "#... " prefix); empty for intro
	Level   int    // heading level (1 for #, 2 for ##, etc.); 0 for intro
	Body    string // full section text including the heading line
}

// parseHeading checks whether line is a markdown heading. Returns the level
// (number of # characters), the heading text, and true if it is a heading.
func parseHeading(line string) (level int, text string, ok bool) {
	if len(line) == 0 || line[0] != '#' {
		return 0, "", false
	}
	i := 0
	for i < len(line) && line[i] == '#' {
		i++
	}
	if i < len(line) && line[i] == ' ' {
		return i, strings.TrimSpace(line[i+1:]), true
	}
	return 0, "", false
}

// ParseSections splits markdown content into sections at any heading boundary
// (#, ##, ###, …). Content before the first heading is the intro section
// (empty Heading, Level 0). Each section's Body includes the heading line and
// everything until the next heading of any level, or end of content.
func ParseSections(content string) []Section {
	lines := strings.SplitAfter(content, "\n")
	var sections []Section
	var cur strings.Builder
	curHeading := ""
	curLevel := 0
	inIntro := true

	flush := func() {
		text := cur.String()
		if inIntro {
			if strings.TrimSpace(text) != "" {
				sections = append(sections, Section{Heading: "", Level: 0, Body: text})
			}
			inIntro = false
		} else {
			sections = append(sections, Section{Heading: curHeading, Level: curLevel, Body: text})
		}
		cur.Reset()
	}

	for _, line := range lines {
		trimmed := strings.TrimRight(line, "\r\n")
		if level, after, ok := parseHeading(trimmed); ok {
			flush()
			curHeading = after
			curLevel = level
		}
		cur.WriteString(line)
	}
	// Flush last section.
	if cur.Len() > 0 || !inIntro {
		flush()
	}
	return sections
}

// normalizeHeadingKey strips any leading '#' characters and lowercases the
// result so callers can pass "## Section Name" or just "Section Name".
func normalizeHeadingKey(h string) string {
	h = strings.TrimSpace(h)
	h = strings.TrimLeft(h, "#")
	return strings.ToLower(strings.TrimSpace(h))
}

// ListSectionHeadings returns the heading names in document order, each
// prefixed with its '#' markers so the caller can see the heading level
// (e.g. "# Title", "## Introduction", "### Details").
func ListSectionHeadings(content string) []string {
	sections := ParseSections(content)
	var headings []string
	for _, s := range sections {
		if s.Heading != "" {
			headings = append(headings, strings.Repeat("#", s.Level)+" "+s.Heading)
		}
	}
	return headings
}

// GetSection returns the full text of the section matching heading
// (case-insensitive, leading '#' markers ignored). The returned text includes
// the original heading line. Returns an error if the heading is not found or
// if it matches more than one section.
func GetSection(content, heading string) (string, error) {
	sections := ParseSections(content)
	target := normalizeHeadingKey(heading)
	var matches []Section
	for _, s := range sections {
		if strings.ToLower(s.Heading) == target {
			matches = append(matches, s)
		}
	}
	if len(matches) == 0 {
		return "", sectionNotFoundError(sections, heading)
	}
	if len(matches) > 1 {
		return "", sectionAmbiguousError(heading, len(matches))
	}
	return matches[0].Body, nil
}

// ReplaceSection replaces the body of a section (matched case-insensitively,
// leading '#' markers ignored) while preserving its original heading line.
// newBody should NOT include the heading line — it is preserved automatically.
// Returns an error if the heading is not found or matches more than one section.
func ReplaceSection(content, heading, newBody string) (string, error) {
	sections := ParseSections(content)
	target := normalizeHeadingKey(heading)
	var count int
	for _, s := range sections {
		if strings.ToLower(s.Heading) == target {
			count++
		}
	}
	if count == 0 {
		return "", sectionNotFoundError(sections, heading)
	}
	if count > 1 {
		return "", sectionAmbiguousError(heading, count)
	}
	var out strings.Builder
	for _, s := range sections {
		if strings.ToLower(s.Heading) == target {
			// Preserve original heading line (level + text).
			out.WriteString(strings.Repeat("#", s.Level) + " " + s.Heading + "\n")
			body := strings.TrimRight(newBody, "\n")
			if body != "" {
				out.WriteString(body + "\n")
			}
		} else {
			out.WriteString(s.Body)
		}
	}
	return out.String(), nil
}

func sectionAmbiguousError(heading string, count int) error {
	return fmt.Errorf("section '%s' is ambiguous (found %d matches) — use search-and-replace mode (old_text/new_text) to target the right occurrence", heading, count)
}

func sectionNotFoundError(sections []Section, heading string) error {
	var names []string
	for _, s := range sections {
		if s.Heading != "" {
			names = append(names, strings.Repeat("#", s.Level)+" "+s.Heading)
		}
	}
	if len(names) == 0 {
		return fmt.Errorf("section not found: '%s' (page has no headings)", heading)
	}
	return fmt.Errorf("section not found: '%s'. Available sections: %s", heading, strings.Join(names, ", "))
}
