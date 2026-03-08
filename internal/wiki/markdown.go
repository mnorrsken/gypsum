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

var wikiLinkPattern = regexp.MustCompile(`\[\[([^\]]+)\]\]`)
var secureAesMacroPattern = regexp.MustCompile(`\{\{secure_aes:([\w+/=]+)\}\}`)

// imageSizePattern matches ![alt|SIZE](url) where SIZE is one of:
//   - 500      → max-width: 500px
//   - 50%      → max-width: 50%
//   - 500x300  → width: 500px; height: 300px
var imageSizePattern = regexp.MustCompile(`!\[([^\]]*)\|(\d+(?:%|x\d+)?)\]\(([^)]*)\)`)

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
	withLinks := wikiLinkPattern.ReplaceAllStringFunc(source, func(match string) string {
		captures := wikiLinkPattern.FindStringSubmatch(match)
		if len(captures) < 2 {
			return match
		}
		title := strings.TrimSpace(captures[1])
		slug := SlugFromTitle(title)
		// Encode the original title so new-page creation can recover it.
		return fmt.Sprintf("[%s](/wiki/%s?title=%s)", title, slug, url.QueryEscape(title))
	})

	// Replace sized image macros: ![alt|SIZE](url) → raw <img> with inline style.
	// SIZE formats: 500 (max-width px), 50% (max-width %), 500x300 (width×height px).
	withLinks = imageSizePattern.ReplaceAllStringFunc(withLinks, func(match string) string {
		caps := imageSizePattern.FindStringSubmatch(match)
		if len(caps) < 4 {
			return match
		}
		alt, size, url := caps[1], caps[2], caps[3]
		var style string
		if strings.HasSuffix(size, "%") {
			style = fmt.Sprintf("max-width:%s;height:auto", size)
		} else if idx := strings.Index(size, "x"); idx != -1 {
			w, h := size[:idx], size[idx+1:]
			style = fmt.Sprintf("width:%spx;height:%spx", w, h)
		} else {
			style = fmt.Sprintf("max-width:%spx;height:auto", size)
		}
		return fmt.Sprintf(`<img src="%s" alt="%s" style="%s">`, url, alt, style)
	})

	// Replace secure_aes macros with placeholder tokens before goldmark so they
	// don't get wrapped in their own <p> blocks. The tokens survive HTML
	// rendering and are swapped for real HTML afterwards.
	var securePlaceholders []string
	withPlaceholders := secureAesMacroPattern.ReplaceAllStringFunc(withLinks, func(match string) string {
		captures := secureAesMacroPattern.FindStringSubmatch(match)
		if len(captures) < 2 {
			return match
		}
		idx := len(securePlaceholders)
		securePlaceholders = append(securePlaceholders, captures[1])
		return fmt.Sprintf("SECURE_PLACEHOLDER_%d", idx)
	})

	var rendered bytes.Buffer
	if err := r.engine.Convert([]byte(withPlaceholders), &rendered); err != nil {
		return "", err
	}

	result := rendered.String()
	for i, ciphertext := range securePlaceholders {
		placeholder := fmt.Sprintf("SECURE_PLACEHOLDER_%d", i)
		replacement := fmt.Sprintf(
			`<span class="secure-inline" data-ciphertext="%s" title="Click to reveal">🔒****</span>`,
			ciphertext,
		)
		result = strings.Replace(result, placeholder, replacement, 1)
	}

	return template.HTML(result), nil
}
