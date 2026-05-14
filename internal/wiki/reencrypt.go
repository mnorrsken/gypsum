package wiki

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// RunReencrypt re-encrypts every {{secure_aes:...}} field across all pages in
// a directory using a new passphrase. It is invoked by the `re-encrypt`
// subcommand of the gypsum binary.
//
// Returns a non-zero exit code when at least one field could not be decrypted
// with the old key.
func RunReencrypt(args []string) int {
	stdout := os.Stdout
	stderr := os.Stderr

	fs := flag.NewFlagSet("re-encrypt", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "path to pages directory (e.g. data/repo/pages)")
	oldKey := fs.String("old-key", "", "current encryption passphrase")
	newKey := fs.String("new-key", "", "new encryption passphrase")
	dryRun := fs.Bool("dry-run", false, "show what would change without writing files")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	if *dir == "" || *oldKey == "" || *newKey == "" {
		fmt.Fprintln(stderr, "Usage: gypsum re-encrypt -dir <pages-dir> -old-key <old> -new-key <new> [-dry-run]")
		return 1
	}

	if *oldKey == *newKey {
		fmt.Fprintln(stderr, "error: old and new keys are the same")
		return 1
	}

	return reencryptDir(*dir, *oldKey, *newKey, *dryRun, stdout, stderr)
}

func reencryptDir(dir, oldKey, newKey string, dryRun bool, stdout, stderr io.Writer) int {
	oldCrypto := NewServerCrypto(oldKey)
	newCrypto := NewServerCrypto(newKey)

	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(stderr, "error reading directory: %v\n", err)
		return 1
	}

	var totalFiles, totalFields, failedFields int

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(stderr, "error reading %s: %v\n", entry.Name(), err)
			continue
		}

		text := string(content)
		if !strings.Contains(text, "{{secure_aes:") {
			continue
		}

		decrypted := oldCrypto.DecryptForEdit(text)

		remaining := strings.Count(decrypted, "{{secure_aes:")
		if remaining > 0 {
			fmt.Fprintf(stderr, "warning: %s has %d field(s) that failed to decrypt with the old key\n", entry.Name(), remaining)
			failedFields += remaining
		}

		reencrypted, err := newCrypto.EncryptForSave(decrypted)
		if err != nil {
			fmt.Fprintf(stderr, "error re-encrypting %s: %v\n", entry.Name(), err)
			continue
		}

		fields := strings.Count(reencrypted, "{{secure_aes:") - remaining
		totalFields += fields
		totalFiles++

		if dryRun {
			fmt.Fprintf(stdout, "[dry-run] %s: %d field(s) would be re-encrypted\n", entry.Name(), fields)
		} else {
			if err := os.WriteFile(path, []byte(reencrypted), 0o644); err != nil {
				fmt.Fprintf(stderr, "error writing %s: %v\n", entry.Name(), err)
				continue
			}
			fmt.Fprintf(stdout, "%s: %d field(s) re-encrypted\n", entry.Name(), fields)
		}
	}

	if dryRun {
		fmt.Fprintf(stdout, "\n[dry-run] Would re-encrypt %d field(s) across %d file(s)\n", totalFields, totalFiles)
	} else {
		fmt.Fprintf(stdout, "\nRe-encrypted %d field(s) across %d file(s)\n", totalFields, totalFiles)
	}
	if failedFields > 0 {
		fmt.Fprintf(stderr, "Warning: %d field(s) could not be decrypted and were left unchanged\n", failedFields)
		return 1
	}
	return 0
}
