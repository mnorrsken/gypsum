# Welcome to Gypsum

This is the starter page for your wiki.

## Wiki links

Use double bracket notation to link pages:

- [[Scratch Pad]]
- [[Architecture Notes]]

## Markdown + code highlighting

```go
package main

import "fmt"

func main() {
    fmt.Println("hello wiki")
}
```

```plaintext
This is plaintext shown in a code block.
You can use fenced blocks with language labels.
```

## Secure plaintext macro

Use this notation in any page:

{{plain:my secret notes}}

It creates an encrypted inline field. Secure content is encrypted on save and decrypted on edit.
