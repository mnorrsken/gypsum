# Welcome to Gypsum

This is the starter page for your wiki. Edit it or create new pages using the sidebar.

## Wiki links

Link pages with double-bracket notation:

- [[Scratch Pad]]
- [[Architecture Notes]]

Prefix with a backslash to display a link literally without following it: \[[Not a Link]]

## Markdown + code highlighting

```go
package main

import "fmt"

func main() {
    fmt.Println("hello wiki")
}
```

Content inside code spans and fenced blocks is shown as-is — wiki links and macros inside them are not processed.

## Secure fields

Store sensitive values inline:

```
API key: {{secure:my-api-key}}
```

On save it encrypts to `{{secure_aes:…}}` and renders as 🔒****. Click to reveal.

Prefix with `\` to display a secure macro literally without encrypting it: \{{secure:example}}

## Images

Paste, drag-and-drop, or use the **Images** toolbar button to upload. Add a size hint to the alt text to control display width:

```
![screenshot|500](/images/example.png)   ← max-width 500 px
![diagram|50%](/images/example.png)      ← max-width 50%
![chart|800x400](/images/example.png)    ← fixed 800×400 px
```

## Skills

Skills are procedural instructions for AI assistants. An example skill has been loaded — open the **Skills** section in the sidebar to view it. Create your own with **+ New Skill** to encode your conventions so AI tools follow them automatically.
