# Writing Good Wiki Pages

Keep wiki pages focused, well-structured, and easy for both humans and AI assistants to navigate.

## When to Use

When creating or editing any wiki page in this Gypsum wiki.

## Instructions

- Start every page with a level-1 heading (`# Page Title`) as the first line — this becomes the display title
- Keep each page focused on one topic; break large topics into linked sub-pages
- Always link a new page from at least one parent (e.g. Home or a category page) using `[[Page Title]]` so it is discoverable
- Use `## Headings` to divide content into scannable sections; put the most important information near the top
- For sensitive data (passwords, tokens, API keys), use `{{secure:your secret}}` — it encrypts on save and can be revealed by clicking the lock icon
- Prefer `[[Page Title]]` wiki links over bare URLs when linking to other pages in the same wiki
- Use backslash to show a wiki link or macro literally without processing: `\[[Page Title]]` or `\{{secure:example}}`
- Content inside backtick code spans and fenced code blocks is displayed as-is — macros and wiki links inside them are not processed

Tags: wiki, writing, pages, structure, formatting, gypsum
