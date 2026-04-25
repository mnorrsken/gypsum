// Command rekey re-encrypts all {{secure_aes:...}} fields across every page
// in a Gypsum data directory. Use this to rotate the encryption passphrase
// (the same passphrase you enter in the browser unlock dialog).
//
// Usage:
//
//	rekey -dir data/repo/pages -old-key "old passphrase" -new-key "new passphrase"
//	rekey -dir data/repo/pages -old-key "old passphrase" -new-key "new passphrase" -dry-run
//
// The same logic is also reachable from the main gypsum binary as
// `gypsum rekey ...` (or by invoking the binary as `rekey`).
package main

import (
	"os"

	"github.com/mnorrsken/gypsum/internal/wiki"
)

func main() {
	os.Exit(wiki.RunRekey(os.Args[1:]))
}
