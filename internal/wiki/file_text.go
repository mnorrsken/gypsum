package wiki

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// maxExtractedText caps how much text is kept from one file for search and
// for get_file.
const maxExtractedText = 2 << 20

// textFileExts are uploads whose bytes are already plain text.
var textFileExts = map[string]bool{".txt": true, ".md": true, ".csv": true, ".json": true}

// ExtractFileText returns the searchable text of a stored upload: the text
// layer of a PDF or the content of a text file. Other types have no text
// and return "".
func ExtractFileText(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case ext == ".pdf":
		return extractPDFText(path)
	case textFileExts[ext]:
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, maxExtractedText))
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", filepath.Base(path), err)
		}
		return strings.ToValidUTF8(string(data), ""), nil
	}
	return "", nil
}

// extractPDFText reads the text layer of a PDF. Scanned PDFs without a text
// layer return "". The parser can panic on malformed files, so a panic is
// turned into an error.
func extractPDFText(path string) (text string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("parsing pdf %s: %v", filepath.Base(path), r)
		}
	}()
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	r, err := pdf.NewReader(f, info.Size())
	if err != nil {
		return "", fmt.Errorf("parsing pdf %s: %w", filepath.Base(path), err)
	}
	plain, err := r.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("reading pdf text %s: %w", filepath.Base(path), err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, io.LimitReader(plain, maxExtractedText)); err != nil {
		return "", fmt.Errorf("reading pdf text %s: %w", filepath.Base(path), err)
	}
	return tidyPDFText(strings.ToValidUTF8(buf.String(), "")), nil
}

// tidyPDFText collapses the space padding PDF text layers use for layout:
// runs of spaces become one, lines are trimmed, and blank lines are merged.
func tidyPDFText(s string) string {
	var out []string
	blank := false
	for _, line := range strings.Split(s, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			if !blank && len(out) > 0 {
				out = append(out, "")
			}
			blank = true
			continue
		}
		out = append(out, line)
		blank = false
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// truncateText cuts s to at most n bytes on a rune boundary.
func truncateText(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

// IndexFile adds or refreshes one stored file in the file search index.
func (s *PageStore) IndexFile(name string) {
	if s.db == nil {
		return
	}
	s.fileIndexMu.Lock()
	defer s.fileIndexMu.Unlock()
	s.indexFileLocked(name)
}

func (s *PageStore) indexFileLocked(name string) {
	path := filepath.Join(s.filesDir, name)
	info, err := os.Stat(path)
	if err != nil {
		_ = s.db.RemoveFile(name)
		return
	}
	text, err := ExtractFileText(path)
	if err != nil {
		log.Printf("files: %v", err)
	}
	if err := s.db.IndexFile(name, text, info.Size(), info.ModTime().Unix()); err != nil {
		log.Printf("files: indexing %s: %v", name, err)
	}
}

// RemoveFileIndex drops a deleted file from the file search index.
func (s *PageStore) RemoveFileIndex(name string) {
	if s.db == nil {
		return
	}
	s.fileIndexMu.Lock()
	defer s.fileIndexMu.Unlock()
	_ = s.db.RemoveFile(name)
}

// SyncFileIndex brings the file search index in line with the files
// directory, extracting text only for new or changed files. Run it at
// startup and after a git pull.
func (s *PageStore) SyncFileIndex() {
	if s.db == nil {
		return
	}
	s.fileIndexMu.Lock()
	defer s.fileIndexMu.Unlock()

	indexed, err := s.db.IndexedFiles()
	if err != nil {
		log.Printf("files: reading index: %v", err)
		return
	}
	files, err := s.ListFiles()
	if err != nil {
		log.Printf("files: listing: %v", err)
		return
	}
	for _, f := range files {
		state, ok := indexed[f.Name]
		delete(indexed, f.Name)
		if ok && state == [2]int64{f.Size, f.ModTime.Unix()} {
			continue
		}
		s.indexFileLocked(f.Name)
	}
	for name := range indexed {
		_ = s.db.RemoveFile(name)
	}
}

// SearchFiles full-text searches file names and extracted file text.
func (s *PageStore) SearchFiles(query string) ([]SearchResult, error) {
	if s.db == nil {
		return nil, nil
	}
	hits, err := s.db.SearchFTSFiles(query)
	if err != nil {
		return nil, err
	}
	results := make([]SearchResult, len(hits))
	for i, h := range hits {
		results[i] = SearchResult{Slug: h.Slug, Title: h.Title, Snippets: h.Snippets}
		if len(h.Snippets) > 0 {
			results[i].Excerpt = h.Snippets[0]
		}
	}
	return results, nil
}

// fileSearchTitle turns a stored filename into words FTS can match:
// "tax-return-20261012-ab12cd34.pdf" → "tax return 20261012 ab12cd34 pdf".
func fileSearchTitle(name string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || r == '.' {
			return ' '
		}
		return r
	}, name)
}
