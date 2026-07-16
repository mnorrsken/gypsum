# Quick Notes

Quick Notes is a whiteboard-style board of short, sticky-note jottings — a middle ground between a full wiki page and a throwaway post-it. Open **Quick Notes** in the sidebar and just start typing; there is no separate "edit" mode and no save button.

## How it works

- **Always editable.** Every note is a live text box. Type into the dashed "New note…" card to start a new note; a fresh blank card appears as soon as the first one is saved.
- **Autosave.** Edits are saved automatically about two seconds after you stop typing (and immediately when a card loses focus or you switch away from the tab). Each save is committed to git, so the full history of every note is preserved.
- **First line is the title.** There is no title field — the first non-empty line of a note is its title. It drives the note's color and how the note appears in MCP listings.
- **Stable colors.** Each card's color is derived by hashing its title, so a given title always gets the same color. Changing the first line re-colors the card.
- **Archive vs delete.** *Archive* moves a note off the board while keeping it in git and searchable (see the archived board via **View archived**). *Delete* removes it permanently.

## Storage

Notes are plain markdown files in the git repo, so they stay readable and diffable outside the app:

```
data/repo/notes/20260716-153042.md          # active note
data/repo/notes/archive/20260701-090500.md   # archived note
```

The file name is the note's id — a creation timestamp (`yyyymmdd-hhmmss`, with a numeric suffix on the rare same-second collision). Because the id never changes, autosave never renames a file when you edit the title. The created date comes from the id; the last-updated date comes from the file's modification time.

## MCP

Notes are fully accessible over the [MCP server](mcp.md) through the `notes` tool section: `list_notes` (with optional full-text `query` and `include_archived`), `get_note`, `create_note`, `edit_note`, `archive_note`, and `delete_note`. AI assistants address notes by their id.

## Concurrency

Notes use a last-write-wins model with no live refresh. If the same note is edited in two browser tabs at once, or over MCP while the board is open, the most recent save wins and the other view will not update until reloaded. This is fine for quick personal notes, but it means the board is not a real-time collaborative surface.
