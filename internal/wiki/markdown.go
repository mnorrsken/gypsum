package wiki

import (
	"bytes"
	"fmt"
	stdhtml "html"
	"html/template"
	"net/url"
	"regexp"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/microcosm-cc/bluemonday"
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

// secureAesMacroRenderRe matches both {{secure_aes:...}} (legacy SHA-256) and
// {{secure_aes2:...}} (PBKDF2). Group 1 is an optional leading backslash so the
// macro can be rendered as literal text; group 2 is the variant ("" or "2");
// group 3 is the base64 payload.
var secureAesMacroRenderRe = regexp.MustCompile(`(\\?)\{\{secure_aes(2?):([\w+/=]+)\}\}`)

// codeSegmentRe matches fenced code blocks (3+ backticks or tildes) and inline
// code spans. Used to protect code regions from Gypsum-specific substitutions.
var codeSegmentRe = regexp.MustCompile(
	"(?m)^(?:`{3,}|~{3,})[^\\n]*\\n[\\s\\S]*?\\n^(?:`{3,}|~{3,})[ \\t]*$" +
		"|`+[^`\\n]+`+",
)

// customEscapeInCodeRe matches backslash-escaped custom macros inside code
// regions so the leading backslash can be stripped (leaving literal text)
// without expanding the macro itself.
var customEscapeInCodeRe = regexp.MustCompile(`\\(\[\[[^\]]+\]\]|\{\{secure_aes2?:[\w+/=]+\}\}|\{\{secure:[^}]*\}\})`)

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
	// publicPolicy sanitizes rendered HTML for anonymous viewers (public
	// shares, docs). The engine allows raw HTML (html.WithUnsafe) for
	// authenticated pages; public output must not execute author HTML/JS.
	publicPolicy *bluemonday.Policy
}

// newPublicPolicy builds the sanitizer for public pages: standard UGC rules
// plus the attributes goldmark's extensions and the image-size macro emit.
func newPublicPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	// Sized images: ![alt|500x300](url) expands to <img style="width:...">.
	p.AllowStyles("width", "height", "max-width").OnElements("img")
	p.AllowAttrs("alt").OnElements("img")
	// Chroma syntax-highlighting spans and goldmark wrapper classes.
	p.AllowAttrs("class").OnElements("span", "code", "pre", "div", "p", "a", "sup", "section", "ol", "ul", "li", "img", "table")
	// Auto heading anchors and footnote references.
	p.AllowAttrs("id").OnElements("h1", "h2", "h3", "h4", "h5", "h6", "sup", "li", "section")
	p.AllowAttrs("role").OnElements("a", "li", "section")
	// GFM task-list checkboxes (rendered disabled).
	p.AllowAttrs("type").Matching(regexp.MustCompile(`^checkbox$`)).OnElements("input")
	p.AllowAttrs("checked", "disabled").OnElements("input")
	return p
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

	return &MarkdownRenderer{engine: engine, publicPolicy: newPublicPolicy()}
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

	// Replace secure_aes / secure_aes2 macros with placeholder tokens before
	// goldmark so they don't get wrapped in their own <p> blocks. The tokens
	// survive HTML rendering and are swapped for real HTML afterwards.
	type securePlaceholder struct{ ciphertext, variant string }
	var securePlaceholders []securePlaceholder
	withPlaceholders := applyOutsideCode(withLinks, func(s string) string {
		return secureAesMacroRenderRe.ReplaceAllStringFunc(s, func(match string) string {
			captures := secureAesMacroRenderRe.FindStringSubmatch(match)
			if len(captures) < 4 {
				return match
			}
			if captures[1] == `\` {
				return "{{secure_aes" + captures[2] + ":" + captures[3] + "}}"
			}
			idx := len(securePlaceholders)
			securePlaceholders = append(securePlaceholders, securePlaceholder{captures[3], captures[2]})
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
	for i, ph := range securePlaceholders {
		placeholder := fmt.Sprintf("SECURE_PLACEHOLDER_%d", i)
		replacement := fmt.Sprintf(
			`<span class="secure-inline" data-ciphertext="%s" data-variant="%s" title="Click to reveal">🔒****</span>`+
				`<button class="secure-copy-btn" data-ciphertext="%s" data-variant="%s" title="Copy to clipboard">📋</button>`,
			ph.ciphertext, ph.variant, ph.ciphertext, ph.variant,
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
			if len(captures) >= 4 && captures[1] == `\` {
				return "{{secure_aes" + captures[2] + ":" + captures[3] + "}}"
			}
			return ""
		})
	})

	withLinks = stripCustomEscapesInCode(withLinks)

	var rendered bytes.Buffer
	if err := r.engine.Convert([]byte(withLinks), &rendered); err != nil {
		return "", err
	}

	// Anonymous viewers must not execute author-supplied HTML/JS: the engine
	// renders raw HTML (html.WithUnsafe), so sanitize the public output.
	return template.HTML(r.publicPolicy.SanitizeBytes(rendered.Bytes())), nil
}

// expandImageSizeMacros replaces ![alt|SIZE](url) with raw <img> tags.
func (r *MarkdownRenderer) expandImageSizeMacros(source string) string {
	return imageSizePattern.ReplaceAllStringFunc(source, func(match string) string {
		caps := imageSizePattern.FindStringSubmatch(match)
		if len(caps) < 4 {
			return match
		}
		alt, size, imgURL := caps[1], caps[2], caps[3]
		if !safeImageURL(imgURL) {
			return match
		}
		var style string
		if strings.HasSuffix(size, "%") {
			style = fmt.Sprintf("max-width:%s;height:auto", size)
		} else if idx := strings.Index(size, "x"); idx != -1 {
			w, h := size[:idx], size[idx+1:]
			style = fmt.Sprintf("width:%spx;height:%spx", w, h)
		} else {
			style = fmt.Sprintf("max-width:%spx;height:auto", size)
		}
		return fmt.Sprintf(`<img src="%s" alt="%s" style="%s">`,
			stdhtml.EscapeString(imgURL), stdhtml.EscapeString(alt), style)
	})
}

// safeImageURL reports whether an image-size macro URL is http(s) or relative.
// Anything else (javascript:, data:, unparseable) is left as literal text.
func safeImageURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return u.Scheme == "" || u.Scheme == "http" || u.Scheme == "https"
}
