package wiki

import (
	"bytes"
	"fmt"
	"html/template"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

var wikiLinkPattern = regexp.MustCompile(`\[\[([^\]]+)\]\]`)
var secureMacroPattern = regexp.MustCompile(`\{\{secure:([\w+/=]+)\}\}`)

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
				highlighting.WithStyle("github"),
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

func (r *MarkdownRenderer) Render(source string) (template.HTML, error) {
	withLinks := wikiLinkPattern.ReplaceAllStringFunc(source, func(match string) string {
		captures := wikiLinkPattern.FindStringSubmatch(match)
		if len(captures) < 2 {
			return match
		}
		title := captures[1]
		slug := SlugFromTitle(title)
		return fmt.Sprintf("[%s](/wiki/%s)", title, slug)
	})

	// Replace secure macros with placeholder tokens before goldmark so they
	// don't get wrapped in their own <p> blocks. The tokens survive HTML
	// rendering and are swapped for real HTML afterwards.
	var securePlaceholders []string
	withPlaceholders := secureMacroPattern.ReplaceAllStringFunc(withLinks, func(match string) string {
		captures := secureMacroPattern.FindStringSubmatch(match)
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
