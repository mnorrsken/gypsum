package wiki

import (
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// RunReencrypt re-encrypts every secure field across all pages in a directory,
// rotating the passphrase and/or salt and upgrading legacy {{secure_aes:...}}
// (SHA-256 KDF) blocks to {{secure_aes2:...}} (PBKDF2). It is invoked by the
// `re-encrypt` subcommand of the gypsum binary.
//
// All output blocks are written as {{secure_aes2:...}} using -new-key and
// -new-salt. Existing {{secure_aes2:...}} blocks are decrypted with -old-key and
// -old-salt; legacy {{secure_aes:...}} blocks are decrypted with -old-key alone.
//
// Returns a non-zero exit code when at least one field could not be decrypted.
func RunReencrypt(args []string) int {
	stdout := os.Stdout
	stderr := os.Stderr

	fs := flag.NewFlagSet("re-encrypt", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "path to pages directory (e.g. data/repo/pages)")
	oldKey := fs.String("old-key", "", "current encryption passphrase")
	newKey := fs.String("new-key", "", "new encryption passphrase")
	oldSalt := fs.String("old-salt", "", "base64 salt for decrypting existing secure_aes2 blocks (omit if none)")
	newSalt := fs.String("new-salt", "", "base64 salt for the re-encrypted secure_aes2 blocks (GYPSUM_SECURE_SALT)")
	iterations := fs.Int("iterations", SecurePBKDF2Iterations, "PBKDF2 iteration count")
	dryRun := fs.Bool("dry-run", false, "show what would change without writing files")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	if *dir == "" || *oldKey == "" || *newKey == "" || *newSalt == "" {
		fmt.Fprintln(stderr, "Usage: gypsum re-encrypt -dir <pages-dir> -old-key <old> -new-key <new> -new-salt <base64> [-old-salt <base64>] [-iterations N] [-dry-run]")
		return 1
	}

	newSaltBytes, err := base64.StdEncoding.DecodeString(*newSalt)
	if err != nil {
		fmt.Fprintf(stderr, "error: -new-salt is not valid base64: %v\n", err)
		return 1
	}
	var oldSaltBytes []byte
	if *oldSalt != "" {
		oldSaltBytes, err = base64.StdEncoding.DecodeString(*oldSalt)
		if err != nil {
			fmt.Fprintf(stderr, "error: -old-salt is not valid base64: %v\n", err)
			return 1
		}
	}

	return reencryptDir(*dir, *oldKey, *newKey, oldSaltBytes, newSaltBytes, *iterations, *dryRun, stdout, stderr)
}

func reencryptDir(dir, oldKey, newKey string, oldSalt, newSalt []byte, iterations int, dryRun bool, stdout, stderr io.Writer) int {
	oldLegacy := NewServerCrypto(oldKey)
	newAes2, err := NewServerCryptoPBKDF2(newKey, newSalt, iterations)
	if err != nil {
		fmt.Fprintf(stderr, "error deriving new key: %v\n", err)
		return 1
	}
	// oldAes2 is only needed when existing secure_aes2 blocks must be decrypted.
	var oldAes2 *ServerCrypto
	if len(oldSalt) > 0 {
		oldAes2, err = NewServerCryptoPBKDF2(oldKey, oldSalt, iterations)
		if err != nil {
			fmt.Fprintf(stderr, "error deriving old key: %v\n", err)
			return 1
		}
	}

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
		if !strings.Contains(text, "{{secure_aes:") && !strings.Contains(text, "{{secure_aes2:") {
			continue
		}

		// Decrypt both formats to {{secure:plain}}. Order matters only in that
		// secure_aes2 is decrypted with its own key before the legacy pass.
		decrypted := text
		if oldAes2 != nil {
			decrypted = oldAes2.DecryptForEdit(decrypted)
		}
		decrypted = oldLegacy.DecryptForEdit(decrypted)

		remaining := strings.Count(decrypted, "{{secure_aes:") + strings.Count(decrypted, "{{secure_aes2:")
		if remaining > 0 {
			hint := ""
			if oldAes2 == nil && strings.Contains(decrypted, "{{secure_aes2:") {
				hint = " (pass -old-salt to decrypt secure_aes2 blocks)"
			}
			fmt.Fprintf(stderr, "warning: %s has %d field(s) that failed to decrypt%s\n", entry.Name(), remaining, hint)
			failedFields += remaining
		}

		reencrypted, err := newAes2.EncryptForSave(decrypted)
		if err != nil {
			fmt.Fprintf(stderr, "error re-encrypting %s: %v\n", entry.Name(), err)
			continue
		}

		fields := strings.Count(reencrypted, "{{secure_aes2:")
		totalFields += fields
		totalFiles++

		if dryRun {
			fmt.Fprintf(stdout, "[dry-run] %s: %d field(s) would be re-encrypted to secure_aes2\n", entry.Name(), fields)
		} else {
			if err := os.WriteFile(path, []byte(reencrypted), 0o644); err != nil {
				fmt.Fprintf(stderr, "error writing %s: %v\n", entry.Name(), err)
				continue
			}
			fmt.Fprintf(stdout, "%s: %d field(s) re-encrypted to secure_aes2\n", entry.Name(), fields)
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
