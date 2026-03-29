// Command rekey re-encrypts all {{secure_aes:...}} fields across every page
// in a Gypsum data directory. Use this when rotating the GYPSUM_SECRET_KEY.
//
// Usage:
//
//	rekey -dir data/repo/pages -old-key "old passphrase" -new-key "new passphrase"
//	rekey -dir data/repo/pages -old-key "old passphrase" -new-key "new passphrase" -dry-run
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mnorrsken/gypsum/internal/wiki"
)

func main() {
	dir := flag.String("dir", "", "path to pages directory (e.g. data/repo/pages)")
	oldKey := flag.String("old-key", "", "current encryption passphrase")
	newKey := flag.String("new-key", "", "new encryption passphrase")
	dryRun := flag.Bool("dry-run", false, "show what would change without writing files")
	flag.Parse()

	if *dir == "" || *oldKey == "" || *newKey == "" {
		fmt.Fprintln(os.Stderr, "Usage: rekey -dir <pages-dir> -old-key <old> -new-key <new> [-dry-run]")
		os.Exit(1)
	}

	if *oldKey == *newKey {
		fmt.Fprintln(os.Stderr, "error: old and new keys are the same")
		os.Exit(1)
	}

	oldCrypto := wiki.NewServerCrypto(*oldKey)
	newCrypto := wiki.NewServerCrypto(*newKey)

	entries, err := os.ReadDir(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading directory: %v\n", err)
		os.Exit(1)
	}

	var totalFiles, totalFields, failedFields int

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(*dir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading %s: %v\n", entry.Name(), err)
			continue
		}

		text := string(content)
		if !strings.Contains(text, "{{secure_aes:") {
			continue
		}

		// Decrypt with old key, then re-encrypt with new key.
		decrypted := oldCrypto.DecryptForEdit(text)

		// Check if any fields failed to decrypt (they'd remain as {{secure_aes:...}}).
		remaining := strings.Count(decrypted, "{{secure_aes:")
		if remaining > 0 {
			fmt.Fprintf(os.Stderr, "warning: %s has %d field(s) that failed to decrypt with the old key\n", entry.Name(), remaining)
			failedFields += remaining
		}

		reencrypted, err := newCrypto.EncryptForSave(decrypted)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error re-encrypting %s: %v\n", entry.Name(), err)
			continue
		}

		fields := strings.Count(reencrypted, "{{secure_aes:") - remaining
		totalFields += fields
		totalFiles++

		if *dryRun {
			fmt.Printf("[dry-run] %s: %d field(s) would be re-encrypted\n", entry.Name(), fields)
		} else {
			if err := os.WriteFile(path, []byte(reencrypted), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "error writing %s: %v\n", entry.Name(), err)
				continue
			}
			fmt.Printf("%s: %d field(s) re-encrypted\n", entry.Name(), fields)
		}
	}

	if *dryRun {
		fmt.Printf("\n[dry-run] Would re-encrypt %d field(s) across %d file(s)\n", totalFields, totalFiles)
	} else {
		fmt.Printf("\nRe-encrypted %d field(s) across %d file(s)\n", totalFields, totalFiles)
	}
	if failedFields > 0 {
		fmt.Fprintf(os.Stderr, "Warning: %d field(s) could not be decrypted and were left unchanged\n", failedFields)
		os.Exit(1)
	}
}
