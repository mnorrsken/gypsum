package wiki

import (
	"bytes"
	"fmt"
	"html/template"
	"net/url"
	"regexp"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

// wikiLinkPattern matches [[title]] with an optional preceding backslash escape.
var wikiLinkPattern = regexp.MustCompile(`(\\?)\[\[([^\]]+)\]\]`)

// imageSizePattern matches ![alt|SIZE](url) where SIZE is one of:
//   - 500      → max-width: 500px
//   - 50%      → max-width: 50%
//   - 500x300  → width: 500px; height: 300px
var imageSizePattern = regexp.MustCompile(`!\[([^\]]*)\|(\d+(?:%|x\d+)?)\]\(([^)]*)\)`)

// secureAesMacroRenderRe is like secureAesMacroRe but captures an optional
// leading backslash so \{{secure_aes:...}} can be rendered as literal text.
var secureAesMacroRenderRe = regexp.MustCompile(`(\\?)\{\{secure_aes:([\w+/=]+)\}\}`)

// codeSegmentRe matches fenced code blocks (3+ backticks or tildes) and inline
// code spans. Used to protect code regions from Gypsum-specific substitutions.
var codeSegmentRe = regexp.MustCompile(
	"(?m)^(?:`{3,}|~{3,})[^\\n]*\\n[\\s\\S]*?\\n^(?:`{3,}|~{3,})[ \\t]*$" +
		"|`+[^`\\n]+`+",
)

// customEscapeInCodeRe matches backslash-escaped custom macros inside code
// regions so the leading backslash can be stripped (leaving literal text)
// without expanding the macro itself.
var customEscapeInCodeRe = regexp.MustCompile(`\\(\[\[[^\]]+\]\]|\{\{secure_aes:[\w+/=]+\}\}|\{\{secure:[^}]*\}\})`)

// applyOutsideCode applies fn to portions of source that are not inside
// inline code spans or fenced code blocks, leaving code regions unchanged.
func applyOutsideCode(source string, fn func(string) string) string {
	indices := codeSegmentRe.FindAllStringIndex(source, -1)
	if len(indices) == 0 {
		return fn(source)
	}
	var b strings.Builder
	b.Grow(len(source))
	pos := 0
	for _, loc := range indices {
		if loc[0] > pos {
			b.WriteString(fn(source[pos:loc[0]]))
		}
		b.WriteString(source[loc[0]:loc[1]])
		pos = loc[1]
	}
	if pos < len(source) {
		b.WriteString(fn(source[pos:]))
	}
	return b.String()
}

// stripCustomEscapesInCode strips backslash escapes for custom macro patterns
// (wiki links and secure_aes) that appear inside code spans and fenced blocks.
// This allows `\[[Page]]` and `\{{secure_aes:...}}` in code to render without
// the leading backslash, consistent with the escape behaviour outside code.
func stripCustomEscapesInCode(source string) string {
	indices := codeSegmentRe.FindAllStringIndex(source, -1)
	if len(indices) == 0 {
		return source
	}
	var b strings.Builder
	b.Grow(len(source))
	pos := 0
	for _, loc := range indices {
		b.WriteString(source[pos:loc[0]])
		b.WriteString(customEscapeInCodeRe.ReplaceAllString(source[loc[0]:loc[1]], "$1"))
		pos = loc[1]
	}
	b.WriteString(source[pos:])
	return b.String()
}

type MarkdownRenderer struct {
	engine goldmark.Markdown
}

func NewMarkdownRenderer() *MarkdownRenderer {
	engine := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.DefinitionList,
			extension.Footnote,
			extension.Strikethrough,
			extension.Table,
			extension.TaskList,
			highlighting.NewHighlighting(
				highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
				highlighting.WithGuessLanguage(true),
			),
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			html.WithHardWraps(),
			html.WithUnsafe(),
		),
	)

	return &MarkdownRenderer{engine: engine}
}

// ExtractH1Title checks if source begins with a level-1 heading (# …).
// If it does, it returns the heading text and the remainder of the content
// (without that heading line). Otherwise it returns empty strings for both.
func ExtractH1Title(source string) (title, rest string) {
	s := strings.TrimLeft(source, "\r\n")
	nlIdx := strings.Index(s, "\n")
	var firstLine, remaining string
	if nlIdx == -1 {
		firstLine = s
		remaining = ""
	} else {
		firstLine = s[:nlIdx]
		remaining = s[nlIdx+1:]
	}
	after, found := strings.CutPrefix(firstLine, "# ")
	if !found {
		return "", source
	}
	return strings.TrimSpace(after), remaining
}

func (r *MarkdownRenderer) Render(source string) (template.HTML, error) {
	// Apply wiki link substitution outside code spans and fenced code blocks.
	// A leading backslash (\[[Page]]) escapes the link and renders it literally.
	withLinks := applyOutsideCode(source, func(s string) string {
		return wikiLinkPattern.ReplaceAllStringFunc(s, func(match string) string {
			captures := wikiLinkPattern.FindStringSubmatch(match)
			if len(captures) < 3 {
				return match
			}
			if captures[1] == `\` {
				return "[[" + captures[2] + "]]"
			}
			title := strings.TrimSpace(captures[2])
			slug := SlugFromTitle(title)
			// Encode the original title so new-page creation can recover it.
			return fmt.Sprintf("[%s](/wiki/%s?title=%s)", title, slug, url.QueryEscape(title))
		})
	})

	withLinks = applyOutsideCode(withLinks, r.expandImageSizeMacros)

	// Strip backslash from \{{secure:...}} kept in storage as an escape. The
	// content becomes literal {{secure:...}} text; it is not encrypted on render.
	withLinks = applyOutsideCode(withLinks, func(s string) string {
		return secureMacroRe.ReplaceAllStringFunc(s, func(match string) string {
			captures := secureMacroRe.FindStringSubmatch(match)
			if len(captures) < 3 || captures[1] != `\` {
				return match
			}
			return "{{secure:" + captures[2] + "}}"
		})
	})

	// Replace secure_aes macros with placeholder tokens before goldmark so they
	// don't get wrapped in their own <p> blocks. The tokens survive HTML
	// rendering and are swapped for real HTML afterwards.
	var securePlaceholders []string
	withPlaceholders := applyOutsideCode(withLinks, func(s string) string {
		return secureAesMacroRenderRe.ReplaceAllStringFunc(s, func(match string) string {
			captures := secureAesMacroRenderRe.FindStringSubmatch(match)
			if len(captures) < 3 {
				return match
			}
			if captures[1] == `\` {
				return "{{secure_aes:" + captures[2] + "}}"
			}
			idx := len(securePlaceholders)
			securePlaceholders = append(securePlaceholders, captures[2])
			return fmt.Sprintf("SECURE_PLACEHOLDER_%d", idx)
		})
	})

	// Strip backslash escapes for custom macros inside code spans/blocks so
	// `\[[Page]]` and `\{{secure_aes:...}}` render without the leading backslash.
	withPlaceholders = stripCustomEscapesInCode(withPlaceholders)

	var rendered bytes.Buffer
	if err := r.engine.Convert([]byte(withPlaceholders), &rendered); err != nil {
		return "", err
	}

	result := rendered.String()
	for i, ciphertext := range securePlaceholders {
		placeholder := fmt.Sprintf("SECURE_PLACEHOLDER_%d", i)
		replacement := fmt.Sprintf(
			`<span class="secure-inline" data-ciphertext="%s" title="Click to reveal"`+
				` hx-post="/secure-inline/unlock" hx-vals='{"ciphertext":"%s"}'`+
				` hx-swap="innerHTML" hx-trigger="click once">🔒****</span>`+
				`<button class="secure-copy-btn" data-ciphertext="%s" title="Copy to clipboard">📋</button>`,
			ciphertext, ciphertext, ciphertext,
		)
		result = strings.Replace(result, placeholder, replacement, 1)
	}

	return template.HTML(result), nil
}

// RenderPublic renders markdown for public (shared) pages:
// - Wiki links [[Page]] become plain text (not clickable)
// - Secure macros {{secure_aes:...}} are stripped entirely
func (r *MarkdownRenderer) RenderPublic(source string) (template.HTML, error) {
	// Convert wiki links to plain text (no links), skipping code regions.
	withLinks := applyOutsideCode(source, func(s string) string {
		return wikiLinkPattern.ReplaceAllStringFunc(s, func(match string) string {
			captures := wikiLinkPattern.FindStringSubmatch(match)
			if len(captures) < 3 {
				return match
			}
			if captures[1] == `\` {
				return "[[" + captures[2] + "]]"
			}
			return strings.TrimSpace(captures[2])
		})
	})

	withLinks = applyOutsideCode(withLinks, r.expandImageSizeMacros)

	// Strip backslash from \{{secure:...}} kept in storage as an escape.
	withLinks = applyOutsideCode(withLinks, func(s string) string {
		return secureMacroRe.ReplaceAllStringFunc(s, func(match string) string {
			captures := secureMacroRe.FindStringSubmatch(match)
			if len(captures) < 3 || captures[1] != `\` {
				return match
			}
			return "{{secure:" + captures[2] + "}}"
		})
	})

	// Strip secure macros entirely — they must not render on public pages.
	// A leading backslash (\{{secure_aes:...}}) escapes the macro: rendered literally.
	withLinks = applyOutsideCode(withLinks, func(s string) string {
		return secureAesMacroRenderRe.ReplaceAllStringFunc(s, func(match string) string {
			captures := secureAesMacroRenderRe.FindStringSubmatch(match)
			if len(captures) >= 3 && captures[1] == `\` {
				return "{{secure_aes:" + captures[2] + "}}"
			}
			return ""
		})
	})

	withLinks = stripCustomEscapesInCode(withLinks)

	var rendered bytes.Buffer
	if err := r.engine.Convert([]byte(withLinks), &rendered); err != nil {
		return "", err
	}

	return template.HTML(rendered.String()), nil
}

// expandImageSizeMacros replaces ![alt|SIZE](url) with raw <img> tags.
func (r *MarkdownRenderer) expandImageSizeMacros(source string) string {
	return imageSizePattern.ReplaceAllStringFunc(source, func(match string) string {
		caps := imageSizePattern.FindStringSubmatch(match)
		if len(caps) < 4 {
			return match
		}
		alt, size, imgURL := caps[1], caps[2], caps[3]
		var style string
		if strings.HasSuffix(size, "%") {
			style = fmt.Sprintf("max-width:%s;height:auto", size)
		} else if idx := strings.Index(size, "x"); idx != -1 {
			w, h := size[:idx], size[idx+1:]
			style = fmt.Sprintf("width:%spx;height:%spx", w, h)
		} else {
			style = fmt.Sprintf("max-width:%spx;height:auto", size)
		}
		return fmt.Sprintf(`<img src="%s" alt="%s" style="%s">`, imgURL, alt, style)
	})
}
