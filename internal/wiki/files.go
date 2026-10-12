package wiki

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// UploadConfig limits what uploads accept and how photos are stored.
type UploadConfig struct {
	MaxBytes   int64 // largest accepted file
	ImageMaxPx int   // longest side of stored JPEG/PNG images; 0 stores images unchanged
}

// DefaultUploadConfig is used when no upload settings are configured.
var DefaultUploadConfig = UploadConfig{MaxBytes: 20 << 20, ImageMaxPx: 2048}

// UploadError is an upload rejected because of its content (type, size).
// Handlers report it as a client error.
type UploadError struct{ msg string }

func (e *UploadError) Error() string { return e.msg }

func uploadErrorf(format string, args ...any) error {
	return &UploadError{msg: fmt.Sprintf(format, args...)}
}

// uploadKind says where an accepted file type is stored and which sniffed
// content types it may have.
type uploadKind struct {
	image bool
	mime  []string // accepted http.DetectContentType prefixes
}

var (
	imageTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}
	textTypes  = []string{"text/plain"}
	zipTypes   = []string{"application/zip"}
)

// uploadKinds maps each accepted file extension to its kind.
var uploadKinds = map[string]uploadKind{
	".png":  {image: true, mime: imageTypes},
	".jpg":  {image: true, mime: imageTypes},
	".jpeg": {image: true, mime: imageTypes},
	".gif":  {image: true, mime: imageTypes},
	".webp": {image: true, mime: imageTypes},
	".svg":  {image: true, mime: []string{"image/svg+xml", "text/xml"}}, // SVGs may be detected as text/xml
	".pdf":  {mime: []string{"application/pdf"}},
	".txt":  {mime: textTypes},
	".md":   {mime: textTypes},
	".csv":  {mime: textTypes},
	".json": {mime: textTypes},
	".docx": {mime: zipTypes},
	".xlsx": {mime: zipTypes},
	".pptx": {mime: zipTypes},
	".odt":  {mime: zipTypes},
	".ods":  {mime: zipTypes},
	".odp":  {mime: zipTypes},
}

// StoredFile describes a saved upload.
type StoredFile struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Kind     string `json:"kind"` // "image" or "file"
	Size     int64  `json:"size"`
	Markdown string `json:"markdown"` // snippet that embeds or links the file in a page
}

// FilesDir is where non-image uploads (PDFs, documents) are stored.
func (s *PageStore) FilesDir() string {
	return s.filesDir
}

// validUploadName rejects names that could escape the upload directories.
func validUploadName(name string) bool {
	return name != "" && !strings.ContainsAny(name, `/\`) && !strings.Contains(name, "..") && !strings.HasPrefix(name, ".")
}

// LocateUpload finds a stored upload by filename in the files or images
// directory and reports whether it is an image.
func (s *PageStore) LocateUpload(name string) (path string, image bool, err error) {
	if !validUploadName(name) {
		return "", false, uploadErrorf("invalid filename")
	}
	for _, c := range []struct {
		dir   string
		image bool
	}{{s.filesDir, false}, {s.imagesDir, true}} {
		p := filepath.Join(c.dir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, c.image, nil
		}
	}
	return "", false, os.ErrNotExist
}

// readUpload reads an upload body, failing with an UploadError when it is
// larger than max bytes.
func readUpload(r io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, fmt.Errorf("reading upload: %w", err)
	}
	if int64(len(data)) > max {
		return nil, uploadErrorf("file is larger than %d MB", max>>20)
	}
	return data, nil
}

// SaveUpload validates an uploaded file, shrinks and strips metadata from
// photos, and writes it to the images or files directory under a unique
// name. caption becomes the alt text or link text of the returned Markdown.
func (s *PageStore) SaveUpload(origName string, data []byte, cfg UploadConfig, caption string) (StoredFile, error) {
	ext := strings.ToLower(filepath.Ext(origName))
	if ext == ".heic" || ext == ".heif" {
		return StoredFile{}, uploadErrorf("HEIC photos are not supported; convert to JPEG first")
	}
	kind, ok := uploadKinds[ext]
	if !ok {
		return StoredFile{}, uploadErrorf("unsupported file type %q", ext)
	}
	if int64(len(data)) > cfg.MaxBytes {
		return StoredFile{}, uploadErrorf("file is larger than %d MB", cfg.MaxBytes>>20)
	}
	if len(data) == 0 {
		return StoredFile{}, uploadErrorf("file is empty")
	}
	contentType := http.DetectContentType(data)
	if !hasAnyPrefix(contentType, kind.mime) {
		return StoredFile{}, uploadErrorf("file content (%s) does not match its %s extension", contentType, ext)
	}

	dir, urlPrefix, kindName := s.filesDir, "/files/", "file"
	if kind.image {
		dir, urlPrefix, kindName = s.imagesDir, "/images/", "image"
		processed, err := processImage(data, ext, cfg.ImageMaxPx)
		if err != nil {
			return StoredFile{}, err
		}
		data = processed
	}

	name := uniqueUploadName(origName, ext)
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return StoredFile{}, fmt.Errorf("writing %s: %w", name, err)
	}

	f := StoredFile{Name: name, URL: urlPrefix + name, Kind: kindName, Size: int64(len(data))}
	label := strings.TrimSpace(strings.NewReplacer("[", "", "]", "", "\n", " ").Replace(caption))
	if kind.image {
		if label == "" {
			label = uploadAltText(origName)
		}
		f.Markdown = fmt.Sprintf("![%s](%s)", label, f.URL)
	} else {
		if label == "" {
			label = filepath.Base(origName)
		}
		f.Markdown = fmt.Sprintf("[%s](%s)", label, f.URL)
	}
	return f, nil
}

// uniqueUploadName builds {name}-{YYYYMMDD}-{8hex}{ext}, or {YYYYMMDD}-{8hex}{ext}
// when the original name is generic or absent.
func uniqueUploadName(origName, ext string) string {
	randBytes := make([]byte, 4)
	_, _ = rand.Read(randBytes)
	datePart := time.Now().Format("20060102")
	shortRand := hex.EncodeToString(randBytes)
	if namePart := sanitizeImageBasename(origName); namePart != "" {
		return fmt.Sprintf("%s-%s-%s%s", namePart, datePart, shortRand, ext)
	}
	return fmt.Sprintf("%s-%s%s", datePart, shortRand, ext)
}

// uploadAltText derives alt text for an image from its original filename.
func uploadAltText(origName string) string {
	base := sanitizeImageBasename(origName)
	if base == "" {
		return "image"
	}
	return strings.ReplaceAll(base, "-", " ")
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// ListFiles returns the non-image uploads, newest first, with the pages that
// link to each.
func (s *PageStore) ListFiles() ([]ImageInfo, error) {
	entries, err := os.ReadDir(s.filesDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	usage := s.findUploadUsage(s.filesDir, "/files/")
	var files []ImageInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if k, ok := uploadKinds[strings.ToLower(filepath.Ext(entry.Name()))]; !ok || k.image {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, ImageInfo{
			Name:    entry.Name(),
			URL:     "/files/" + entry.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			UsedBy:  usage[entry.Name()],
		})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime.After(files[j].ModTime)
	})
	return files, nil
}

// AppendToPage adds text as a new paragraph at the end of an existing page.
func (s *PageStore) AppendToPage(slug, text string) error {
	page, err := s.Load(KindPage, slug)
	if err != nil {
		return err
	}
	content := strings.TrimRight(page.Content, "\n")
	if content != "" {
		content += "\n\n"
	}
	return s.Save(KindPage, slug, content+text+"\n")
}

// inlineFileExts are served inline; every other file type is a download.
var inlineFileExts = map[string]bool{".pdf": true, ".txt": true, ".md": true, ".csv": true, ".json": true}

// serveUploadedFile serves a stored file. Office documents are sent as
// downloads; text types are served as plain text so they never render as HTML.
func serveUploadedFile(w http.ResponseWriter, r *http.Request, path string) {
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case ext == ".pdf":
		w.Header().Set("Content-Type", "application/pdf")
	case inlineFileExts[ext]:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	default:
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(path)))
	}
	http.ServeFile(w, r, path)
}
