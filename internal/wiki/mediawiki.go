package wiki

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// Pre-compiled regexes for MediaWiki conversion.
var (
	mwReLang      = regexp.MustCompile(`(?i)lang=["']?(\w+)["']?`)
	mwReSyntaxHL  = regexp.MustCompile(`(?si)<syntaxhighlight([^>]*)>(.*?)</syntaxhighlight>`)
	mwReSource    = regexp.MustCompile(`(?si)<source([^>]*)>(.*?)</source>`)
	mwRePre       = regexp.MustCompile(`(?si)<pre>(.*?)</pre>`)
	mwReNowikiPre = regexp.MustCompile(`(?sm)^ <nowiki>(.*?)</nowiki>`)
	mwReNowiki    = regexp.MustCompile(`(?si)<nowiki>(.*?)</nowiki>`)
	mwReCode      = regexp.MustCompile(`(?si)<code>(.*?)</code>`)
	mwReRef       = regexp.MustCompile(`(?si)<ref[^>]*>.*?</ref>`)
	mwReRefSelf   = regexp.MustCompile(`(?i)<ref[^/]*/\s*>`)
	mwReRefs      = regexp.MustCompile(`(?si)<references\s*(?:/>|>.*?</references>)`)
	mwReTmpl      = regexp.MustCompile(`(?s)\{\{[^{}]*\}\}`)
	mwReCat       = regexp.MustCompile(`(?i)\[\[Category:[^\]]*\]\]`)
	mwReMagic     = regexp.MustCompile(`(?i)__(?:TOC|NOTOC|FORCETOC|NOEDITSECTION)__`)
	mwReFile      = regexp.MustCompile(`(?i)\[\[(?:File|Image):([^|\]]+)(?:\|[^\]]*)?\]\]`)
	mwReBr        = regexp.MustCompile(`(?i)<br\s*/?\s*>`)
	mwReHTML       = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)
	mwReBold      = regexp.MustCompile(`'''(.*?)'''`)
	mwReItalic    = regexp.MustCompile(`''(.*?)''`)
	mwReExtLink   = regexp.MustCompile(`\[(https?://\S+)\s+([^\]]+)\]`)
	mwReExtBare   = regexp.MustCompile(`\[(https?://\S+)\]`)
	mwReWikiLink  = regexp.MustCompile(`\[\[([^#|\]]+)(?:#[^|\]]*)?(?:\|[^\]]+)?\]\]`)
	mwReHR        = regexp.MustCompile(`(?m)^-{4,}\s*$`)
	mwRePH        = regexp.MustCompile(`\x00PH(\d+)\x00`)
	mwReBlankLines = regexp.MustCompile(`\n{3,}`)
)

// ConvertMediaWikiToMarkdown converts MediaWiki wikitext to Markdown format.
// It handles headings, bold/italic, code blocks (<syntaxhighlight>, <source>,
// <pre>), <nowiki>, <code>, lists, tables, wiki links, external links, and
// cleans up templates, categories, and references.
func ConvertMediaWikiToMarkdown(input string) string {
	s := strings.ReplaceAll(input, "\r\n", "\n")

	// Placeholder system: code blocks and nowiki content are extracted early
	// and replaced with opaque tokens so that later passes (bold/italic/link
	// conversion, etc.) cannot accidentally mangle them.
	var placeholders []string
	ph := func(content string) string {
		idx := len(placeholders)
		placeholders = append(placeholders, content)
		return fmt.Sprintf("\x00PH%d\x00", idx)
	}

	// ── Phase 1: extract blocks that must not be further processed ──

	// <syntaxhighlight lang="...">...</syntaxhighlight>
	s = mwReSyntaxHL.ReplaceAllStringFunc(s, func(match string) string {
		m := mwReSyntaxHL.FindStringSubmatch(match)
		lang := ""
		if lm := mwReLang.FindStringSubmatch(m[1]); lm != nil {
			lang = lm[1]
		}
		code := trimCodeBlock(html.UnescapeString(m[2]))
		return ph("\n```" + lang + "\n" + code + "\n```\n")
	})

	// <source lang="...">...</source> (older MediaWiki syntax)
	s = mwReSource.ReplaceAllStringFunc(s, func(match string) string {
		m := mwReSource.FindStringSubmatch(match)
		lang := ""
		if lm := mwReLang.FindStringSubmatch(m[1]); lm != nil {
			lang = lm[1]
		}
		code := trimCodeBlock(html.UnescapeString(m[2]))
		return ph("\n```" + lang + "\n" + code + "\n```\n")
	})

	// <pre>...</pre>
	s = mwRePre.ReplaceAllStringFunc(s, func(match string) string {
		m := mwRePre.FindStringSubmatch(match)
		code := trimCodeBlock(html.UnescapeString(m[1]))
		return ph("\n```\n" + code + "\n```\n")
	})

	// <nowiki> preceded by a space at start of line → code block
	s = mwReNowikiPre.ReplaceAllStringFunc(s, func(match string) string {
		m := mwReNowikiPre.FindStringSubmatch(match)
		code := trimCodeBlock(html.UnescapeString(m[1]))
		return ph("\n```\n" + code + "\n```\n")
	})

	// <nowiki>...</nowiki> – literal text, escape markdown special chars.
	s = mwReNowiki.ReplaceAllStringFunc(s, func(match string) string {
		m := mwReNowiki.FindStringSubmatch(match)
		return ph(escapeMarkdownChars(html.UnescapeString(m[1])))
	})

	// ── Phase 2: remove or convert remaining HTML-like elements ──

	// <code>...</code> → inline backticks
	s = mwReCode.ReplaceAllString(s, "`$1`")

	// Remove <ref>...</ref> and self-closing <ref ... />
	s = mwReRef.ReplaceAllString(s, "")
	s = mwReRefSelf.ReplaceAllString(s, "")

	// Remove <references/> or <references>...</references>
	s = mwReRefs.ReplaceAllString(s, "")

	// Remove templates {{...}} – iterate to collapse nested braces.
	for i := 0; i < 10 && mwReTmpl.MatchString(s); i++ {
		s = mwReTmpl.ReplaceAllString(s, "")
	}

	// Remove categories [[Category:...]]
	s = mwReCat.ReplaceAllString(s, "")

	// Remove magic words
	s = mwReMagic.ReplaceAllString(s, "")

	// [[File:name|...]] / [[Image:name|...]] → ![name](name)
	s = mwReFile.ReplaceAllString(s, "![$1]($1)")

	// <br> variants → newline
	s = mwReBr.ReplaceAllString(s, "\n")

	// Strip any remaining HTML tags (but not our \x00 placeholders).
	s = mwReHTML.ReplaceAllString(s, "")

	// ── Phase 3: tables ──

	s = convertMediaWikiTables(s)

	// ── Phase 4: line-by-line (headings, lists, definition lists, indent) ──
	// In MediaWiki, lines starting with a space are preformatted text.
	// Consecutive such lines are grouped into a single fenced code block.

	lines := strings.Split(s, "\n")
	var result []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		// Space-prefixed line → preformatted; collect consecutive run.
		if strings.HasPrefix(line, " ") && strings.TrimSpace(line) != "" {
			var codeLines []string
			for i < len(lines) && strings.HasPrefix(lines[i], " ") && strings.TrimSpace(lines[i]) != "" {
				codeLines = append(codeLines, lines[i][1:]) // strip the leading space
				i++
			}
			i-- // outer loop will i++ again
			result = append(result, "```")
			result = append(result, codeLines...)
			result = append(result, "```")
			continue
		}
		result = append(result, convertMediaWikiLine(line))
	}
	s = strings.Join(result, "\n")

	// ── Phase 5: inline formatting ──

	// Bold: '''...''' → **...**  (process before italic)
	s = mwReBold.ReplaceAllString(s, "**$1**")

	// Italic: ''...'' → *...*
	s = mwReItalic.ReplaceAllString(s, "*$1*")

	// External links with text: [url text] → [text](url)
	s = mwReExtLink.ReplaceAllString(s, "[$2]($1)")

	// Bare external links: [url] → url
	s = mwReExtBare.ReplaceAllString(s, "$1")

	// Wiki links: strip section anchors and piped display text.
	// [[Page#Section|display]] → [[Page]]
	// [[Page|display]]        → [[Page]]
	// [[Page#Section]]        → [[Page]]
	// [[Page]]                → [[Page]]  (unchanged)
	s = mwReWikiLink.ReplaceAllString(s, "[[$1]]")

	// Horizontal rules: ---- → ---
	s = mwReHR.ReplaceAllString(s, "---")

	// ── Phase 6: cleanup ──

	// Decode HTML entities (e.g. &amp; → &)
	s = html.UnescapeString(s)

	// Restore placeholders
	s = mwRePH.ReplaceAllStringFunc(s, func(match string) string {
		m := mwRePH.FindStringSubmatch(match)
		idx, _ := strconv.Atoi(m[1])
		if idx < len(placeholders) {
			return placeholders[idx]
		}
		return match
	})

	// Collapse 3+ consecutive blank lines into 2.
	s = mwReBlankLines.ReplaceAllString(s, "\n\n")

	return strings.TrimSpace(s) + "\n"
}

// trimCodeBlock strips a single leading/trailing newline from code content.
func trimCodeBlock(s string) string {
	s = strings.TrimPrefix(s, "\n")
	s = strings.TrimSuffix(s, "\n")
	return s
}

// escapeMarkdownChars escapes characters that have special meaning in Markdown.
func escapeMarkdownChars(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		"`", "\\`",
		"*", `\*`,
		"_", `\_`,
		"[", `\[`,
		"]", `\]`,
		"{", `\{`,
		"}", `\}`,
		"#", `\#`,
		"|", `\|`,
		"~", `\~`,
		">", `\>`,
	)
	return r.Replace(s)
}

// convertMediaWikiLine converts a single line of MediaWiki markup.
func convertMediaWikiLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}

	// Headings: ====== h6 ====== down to = h1 =
	for level := 6; level >= 1; level-- {
		prefix := strings.Repeat("=", level)
		if strings.HasPrefix(trimmed, prefix) && strings.HasSuffix(trimmed, prefix) {
			inner := strings.TrimSpace(trimmed[level : len(trimmed)-level])
			if inner != "" && !strings.HasPrefix(inner, "=") {
				return strings.Repeat("#", level) + " " + inner
			}
		}
	}

	// Unordered lists: *, **, ***, ...
	if len(trimmed) > 0 && trimmed[0] == '*' {
		depth := 0
		for i := 0; i < len(trimmed) && trimmed[i] == '*'; i++ {
			depth++
		}
		content := strings.TrimSpace(trimmed[depth:])
		indent := strings.Repeat("  ", depth-1)
		return indent + "- " + content
	}

	// Ordered lists: #, ##, ###, ...
	if len(trimmed) > 0 && trimmed[0] == '#' {
		depth := 0
		for i := 0; i < len(trimmed) && trimmed[i] == '#'; i++ {
			depth++
		}
		content := strings.TrimSpace(trimmed[depth:])
		indent := strings.Repeat("  ", depth-1)
		return indent + "1. " + content
	}

	// Definition term: ;term
	if strings.HasPrefix(trimmed, ";") {
		return "**" + strings.TrimSpace(trimmed[1:]) + "**"
	}

	// Indent / blockquote: :text
	if trimmed[0] == ':' {
		depth := 0
		for i := 0; i < len(trimmed) && trimmed[i] == ':'; i++ {
			depth++
		}
		content := strings.TrimSpace(trimmed[depth:])
		return "> " + content
	}

	return line
}

// convertMediaWikiTables finds {| ... |} table blocks and converts them.
func convertMediaWikiTables(s string) string {
	for {
		start := strings.Index(s, "\n{|")
		if start == -1 {
			if strings.HasPrefix(s, "{|") {
				start = 0
			} else {
				break
			}
		} else {
			start++ // skip the leading \n, point at {|
		}

		endMarker := "\n|}"
		endIdx := strings.Index(s[start:], endMarker)
		if endIdx == -1 {
			break
		}
		endIdx = start + endIdx + len(endMarker)

		tableWiki := s[start:endIdx]
		tableMD := convertOneMediaWikiTable(tableWiki)

		s = s[:start] + tableMD + s[endIdx:]
	}
	return s
}

func convertOneMediaWikiTable(wiki string) string {
	lines := strings.Split(wiki, "\n")

	var headers []string
	var rows [][]string
	var currentRow []string
	isHeaderRow := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip table start, caption, end
		if strings.HasPrefix(trimmed, "{|") || strings.HasPrefix(trimmed, "|+") || trimmed == "|}" {
			continue
		}

		// Row separator
		if trimmed == "|-" || strings.HasPrefix(trimmed, "|- ") {
			if currentRow != nil {
				if isHeaderRow && len(headers) == 0 {
					headers = currentRow
				} else {
					rows = append(rows, currentRow)
				}
			}
			currentRow = nil
			isHeaderRow = false
			continue
		}

		// Header cells: ! cell !! cell
		if strings.HasPrefix(trimmed, "!") {
			isHeaderRow = true
			cellText := trimmed[1:]
			cells := strings.Split(cellText, "!!")
			for _, c := range cells {
				currentRow = append(currentRow, strings.TrimSpace(stripTableCellAttrs(c)))
			}
			continue
		}

		// Data cells: | cell || cell
		if strings.HasPrefix(trimmed, "|") {
			cellText := trimmed[1:]
			cells := strings.Split(cellText, "||")
			for _, c := range cells {
				currentRow = append(currentRow, strings.TrimSpace(stripTableCellAttrs(c)))
			}
			continue
		}
	}

	// Save last row
	if currentRow != nil {
		if isHeaderRow && len(headers) == 0 {
			headers = currentRow
		} else {
			rows = append(rows, currentRow)
		}
	}

	if len(headers) == 0 && len(rows) == 0 {
		return ""
	}

	// If no explicit headers, promote first row
	if len(headers) == 0 && len(rows) > 0 {
		headers = rows[0]
		rows = rows[1:]
	}

	numCols := len(headers)

	var sb strings.Builder
	sb.WriteString("| " + strings.Join(headers, " | ") + " |\n")
	sb.WriteString("|")
	for i := 0; i < numCols; i++ {
		sb.WriteString(" --- |")
	}
	sb.WriteString("\n")
	for _, row := range rows {
		for len(row) < numCols {
			row = append(row, "")
		}
		sb.WriteString("| " + strings.Join(row[:numCols], " | ") + " |\n")
	}

	return sb.String()
}

// stripTableCellAttrs removes style/class attributes that precede cell content.
// MediaWiki allows: | style="..." | actual content
func stripTableCellAttrs(cell string) string {
	idx := strings.Index(cell, "|")
	if idx >= 0 {
		before := cell[:idx]
		if strings.Contains(before, "=") {
			return strings.TrimSpace(cell[idx+1:])
		}
	}
	return strings.TrimSpace(cell)
}
