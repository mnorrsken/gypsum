package wiki

import (
	"fmt"
	"strings"
)

// Section represents a portion of markdown content delimited by # headings.
type Section struct {
	Heading string // heading text (without "# " prefix); empty for intro
	Body    string // full section text including the "# Heading" line
}

// ParseSections splits markdown content into sections at H1 (`# `) boundaries.
// Content before the first heading is the intro section (empty Heading).
// Each section's Body includes the `# Heading` line and everything until the
// next `# ` line or end of content.
func ParseSections(content string) []Section {
	lines := strings.SplitAfter(content, "\n")
	var sections []Section
	var cur strings.Builder
	curHeading := ""
	inIntro := true

	flush := func() {
		text := cur.String()
		if inIntro {
			// Only emit intro if non-empty (ignoring whitespace).
			if strings.TrimSpace(text) != "" {
				sections = append(sections, Section{Heading: "", Body: text})
			}
			inIntro = false
		} else {
			sections = append(sections, Section{Heading: curHeading, Body: text})
		}
		cur.Reset()
	}

	for _, line := range lines {
		trimmed := strings.TrimRight(line, "\r\n")
		if after, ok := strings.CutPrefix(trimmed, "# "); ok {
			flush()
			curHeading = strings.TrimSpace(after)
		}
		cur.WriteString(line)
	}
	// Flush last section.
	if cur.Len() > 0 || !inIntro {
		flush()
	}
	return sections
}

// ListSectionHeadings returns the H1 heading names in order.
func ListSectionHeadings(content string) []string {
	sections := ParseSections(content)
	var headings []string
	for _, s := range sections {
		if s.Heading != "" {
			headings = append(headings, s.Heading)
		}
	}
	return headings
}

// GetSection returns the full text of the section matching heading
// (case-insensitive). The returned text includes the `# Heading` line.
func GetSection(content, heading string) (string, error) {
	sections := ParseSections(content)
	target := strings.ToLower(strings.TrimSpace(heading))
	for _, s := range sections {
		if strings.ToLower(s.Heading) == target {
			return s.Body, nil
		}
	}
	return "", sectionNotFoundError(sections, heading)
}

// ReplaceSection replaces the body of a section (matched case-insensitively)
// while preserving its `# Heading` line. newBody should NOT include the heading
// line — it is preserved automatically.
func ReplaceSection(content, heading, newBody string) (string, error) {
	sections := ParseSections(content)
	target := strings.ToLower(strings.TrimSpace(heading))
	found := false
	var out strings.Builder
	for _, s := range sections {
		if strings.ToLower(s.Heading) == target {
			found = true
			// Preserve original heading line.
			out.WriteString("# " + s.Heading + "\n")
			body := strings.TrimRight(newBody, "\n")
			if body != "" {
				out.WriteString(body + "\n")
			}
		} else {
			out.WriteString(s.Body)
		}
	}
	if !found {
		return "", sectionNotFoundError(sections, heading)
	}
	return out.String(), nil
}

func sectionNotFoundError(sections []Section, heading string) error {
	var names []string
	for _, s := range sections {
		if s.Heading != "" {
			names = append(names, s.Heading)
		}
	}
	if len(names) == 0 {
		return fmt.Errorf("section not found: '%s' (page has no # headings)", heading)
	}
	return fmt.Errorf("section not found: '%s'. Available sections: %s", heading, strings.Join(names, ", "))
}
