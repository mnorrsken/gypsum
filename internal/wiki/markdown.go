package wiki

import (
	"bytes"
	"fmt"
	"html/template"
	"regexp"

	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

var wikiLinkPattern = regexp.MustCompile(`\[\[([^\]]+)\]\]`)
var secureMacroPattern = regexp.MustCompile(`\{\{\s*secure:([a-zA-Z0-9_\-]+)\s*\}\}`)

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

func (r *MarkdownRenderer) Render(pageSlug, source string) (template.HTML, error) {
	withLinks := wikiLinkPattern.ReplaceAllStringFunc(source, func(match string) string {
		captures := wikiLinkPattern.FindStringSubmatch(match)
		if len(captures) < 2 {
			return match
		}
		title := captures[1]
		slug := SlugFromTitle(title)
		return fmt.Sprintf("[%s](/wiki/%s)", title, slug)
	})

	withSecureLinks := secureMacroPattern.ReplaceAllStringFunc(withLinks, func(match string) string {
		captures := secureMacroPattern.FindStringSubmatch(match)
		if len(captures) < 2 {
			return match
		}
		blockID := captures[1]
		return fmt.Sprintf(
			`<div class="secure-inline" data-page-slug="%s" data-block-id="%s"><div class="secure-inline-header">🔒 Secure block: %s</div><button type="button" class="button secure-unlock-trigger" data-page-slug="%s" data-block-id="%s">Unlock</button><pre class="secure-inline-content" hidden></pre></div>`,
			pageSlug,
			blockID,
			blockID,
			pageSlug,
			blockID,
		)
	})

	var rendered bytes.Buffer
	if err := r.engine.Convert([]byte(withSecureLinks), &rendered); err != nil {
		return "", err
	}

	return template.HTML(rendered.String()), nil
}
