package wiki

import (
	"fmt"
	"html"
	"html/template"
	"strings"
)

// RenderUnifiedDiff produces a colorized HTML unified diff between old and new content.
func RenderUnifiedDiff(oldContent, newContent, filename string) template.HTML {
	oldLines := splitLines(oldContent)
	newLines := splitLines(newContent)

	// Compute LCS-based diff
	hunks := computeHunks(oldLines, newLines)

	if len(hunks) == 0 {
		return template.HTML(`<div class="diff-container"><p class="diff-empty">No changes.</p></div>`)
	}

	var sb strings.Builder
	sb.WriteString(`<div class="diff-container">`)
	sb.WriteString(fmt.Sprintf(`<div class="diff-header">%s</div>`, html.EscapeString(filename)))

	for _, hunk := range hunks {
		sb.WriteString(fmt.Sprintf(`<div class="diff-hunk-header">@@ -%d,%d +%d,%d @@</div>`,
			hunk.oldStart, hunk.oldCount, hunk.newStart, hunk.newCount))
		for _, line := range hunk.lines {
			escaped := html.EscapeString(line.text)
			switch line.kind {
			case diffContext:
				sb.WriteString(fmt.Sprintf(`<div class="diff-line diff-ctx"> %s</div>`, escaped))
			case diffRemoved:
				sb.WriteString(fmt.Sprintf(`<div class="diff-line diff-del">-%s</div>`, escaped))
			case diffAdded:
				sb.WriteString(fmt.Sprintf(`<div class="diff-line diff-add">+%s</div>`, escaped))
			}
		}
	}

	sb.WriteString(`</div>`)
	return template.HTML(sb.String())
}

type diffKind int

const (
	diffContext diffKind = iota
	diffRemoved
	diffAdded
)

type diffLine struct {
	kind diffKind
	text string
}

type diffHunk struct {
	oldStart int
	oldCount int
	newStart int
	newCount int
	lines    []diffLine
}

type editOp int

const (
	opEqual editOp = iota
	opDelete
	opInsert
)

type edit struct {
	op   editOp
	text string
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// computeHunks produces unified-diff-style hunks with context lines.
func computeHunks(oldLines, newLines []string) []diffHunk {
	const contextLines = 3

	edits := lcsDiff(oldLines, newLines)

	// Group edits into hunks with context
	var hunks []diffHunk
	i := 0
	for i < len(edits) {
		// Skip equal lines until we find a change
		for i < len(edits) && edits[i].op == opEqual {
			i++
		}
		if i >= len(edits) {
			break
		}

		// Start of a hunk — include context before
		start := i
		contextStart := start - contextLines
		if contextStart < 0 {
			contextStart = 0
		}

		// Build hunk lines: leading context
		var lines []diffLine
		for j := contextStart; j < start; j++ {
			lines = append(lines, diffLine{kind: diffContext, text: edits[j].text})
		}

		// Add changed lines, merging adjacent changes separated by <= 2*contextLines equals
		for i < len(edits) {
			if edits[i].op != opEqual {
				switch edits[i].op {
				case opDelete:
					lines = append(lines, diffLine{kind: diffRemoved, text: edits[i].text})
				case opInsert:
					lines = append(lines, diffLine{kind: diffAdded, text: edits[i].text})
				}
				i++
				continue
			}
			// Count consecutive equals
			eqStart := i
			for i < len(edits) && edits[i].op == opEqual {
				i++
			}
			eqLen := i - eqStart

			if i < len(edits) && eqLen <= 2*contextLines {
				// Small gap — include as context and continue the hunk
				for j := eqStart; j < i; j++ {
					lines = append(lines, diffLine{kind: diffContext, text: edits[j].text})
				}
				continue
			}

			// Large gap or end — add trailing context and close hunk
			trailEnd := eqStart + contextLines
			if trailEnd > eqStart+eqLen {
				trailEnd = eqStart + eqLen
			}
			for j := eqStart; j < trailEnd; j++ {
				lines = append(lines, diffLine{kind: diffContext, text: edits[j].text})
			}
			// Reset i so the outer loop can find the next change
			i = eqStart + eqLen
			break
		}

		// Compute line numbers for the hunk header
		oldLine := 1
		newLine := 1
		for j := 0; j < contextStart; j++ {
			switch edits[j].op {
			case opEqual:
				oldLine++
				newLine++
			case opDelete:
				oldLine++
			case opInsert:
				newLine++
			}
		}

		oldCount := 0
		newCount := 0
		for _, l := range lines {
			switch l.kind {
			case diffContext:
				oldCount++
				newCount++
			case diffRemoved:
				oldCount++
			case diffAdded:
				newCount++
			}
		}

		hunks = append(hunks, diffHunk{
			oldStart: oldLine,
			oldCount: oldCount,
			newStart: newLine,
			newCount: newCount,
			lines:    lines,
		})
	}

	return hunks
}

// lcsDiff computes the shortest edit script between two slices of strings
// using an O(N*M) LCS (Longest Common Subsequence) table.
func lcsDiff(a, b []string) []edit {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil
	}

	// Simple O(NM) LCS-based diff for correctness and simplicity
	// Build LCS table
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	// Trace back to produce edit script
	var result []edit
	i, j := 0, 0
	for i < n && j < m {
		if a[i] == b[j] {
			result = append(result, edit{opEqual, a[i]})
			i++
			j++
		} else if lcs[i+1][j] >= lcs[i][j+1] {
			result = append(result, edit{opDelete, a[i]})
			i++
		} else {
			result = append(result, edit{opInsert, b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		result = append(result, edit{opDelete, a[i]})
	}
	for ; j < m; j++ {
		result = append(result, edit{opInsert, b[j]})
	}

	return result
}
