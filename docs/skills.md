# Skills

Skills are procedural knowledge pages optimized for AI retrieval — build instructions, testing conventions, deployment steps, coding patterns. They live in a separate `skills/` directory alongside your wiki pages and have dedicated MCP tools with tag-boosted search.

Unlike regular wiki pages (which document information for humans), skills tell an AI assistant **how** to do something. When an LLM connects to your wiki via MCP, it can search for relevant skills before starting a task and follow the instructions automatically.

## Creating a Skill

Click **+ New Skill** in the sidebar, or use the MCP `create_skill` tool. A skill is just markdown with a recommended structure:

```markdown
# Go Testing Conventions

Use table-driven tests with testify/assert for all Go projects.

## When to Use

When creating or modifying tests in any Go project using modules.

## Instructions

- Use `testify/assert` for assertions
- Always use table-driven tests with `for _, tc := range tests`
- Name test functions `TestXxx_descriptiveName`
- Put test files alongside source as `xxx_test.go`

Tags: go, golang, testing, tests, testify, table-driven
```

The sections serve specific purposes:

- **Top-level heading + summary** — what this skill covers, matched against search queries.
- **When to Use** — tells the LLM when this skill applies. Be specific about languages, frameworks, and task types.
- **Instructions** — the actual steps to follow. Keep these concrete and actionable.
- **Tags:** line — a comma-separated list at the very end. Tag matches are weighted much higher than content matches in search, so choose tags that match how you'd describe the task.

## MCP Tools

| Tool | Description |
|---|---|
| `list_skills` | List all skills with their tags |
| `get_skill` | Read a skill's full markdown content |
| `create_skill` | Create a new skill |
| `edit_skill` | Update an existing skill |
| `delete_skill` | Delete a skill |
| `search_skills` | Search across skills (tag matches ranked highest) |

The `search_skills` tool accepts multiple queries in a single call, so an LLM can search for `["go testing", "makefile"]` and get results for both topics at once.

## Automatic Skill Lookup with Claude Code

The real power of skills is automatic retrieval. Add this to your project's `CLAUDE.md` (or global `~/.claude/CLAUDE.md`) to have Claude Code check for relevant skills before starting any implementation task:

```markdown
## Wiki Skills

Before starting implementation tasks (writing code, tests, builds, deployments, refactoring),
search the Gypsum wiki for relevant skills using `search_skills` with keywords matching
the language/framework and task type. Follow any matching skill instructions.
```

With this in place, when you tell Claude to "write tests for this Go package", it will automatically call `search_skills("go testing")`, find your conventions, and follow them — without you having to repeat your preferences every time.

This works with any LLM that supports MCP, not just Claude Code. The key requirement is that the LLM's system prompt or project instructions tell it to search for skills before acting.

## Tips

- **Be specific in tags.** `go, testing` is better than `code, quality`. Tags should match the words someone would use when describing the task.
- **One skill per concern.** A skill for "Go testing" and a separate skill for "Go build conventions" is better than one giant "Go conventions" skill. This way search returns only what's relevant.
- **Keep instructions actionable.** "Use table-driven tests" is better than "Consider using table-driven tests when appropriate". LLMs follow direct instructions more reliably.
- **Update skills when you correct the LLM.** If you find yourself repeatedly correcting Claude's behavior on a topic, create or update a skill so it gets it right automatically next time.
