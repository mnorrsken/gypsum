package wiki

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// mcpImageMaxPx is the longest side of images returned by get_file; larger
// images cost more tokens without the model seeing more detail.
const mcpImageMaxPx = 1568

// mcpImageMaxBytes is the largest image get_file returns without re-encoding.
const mcpImageMaxBytes = 1 << 20

// mcpFileEntry is one file in list_files output.
type mcpFileEntry struct {
	Name     string    `json:"name"`
	URL      string    `json:"url"`
	Kind     string    `json:"kind"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	UsedBy   []string  `json:"used_by,omitempty"`
}

func (m *MCPHandler) toolListFiles(args map[string]any) mcpCallToolResult {
	if queries, ok := mcpArgStringArray(args, "query"); ok {
		var sb strings.Builder
		seen := make(map[string]bool)
		for _, q := range queries {
			results, err := m.store.SearchFiles(q)
			if err != nil {
				return mcpError("file search failed: " + err.Error())
			}
			for _, r := range results {
				if seen[r.Slug] {
					continue
				}
				seen[r.Slug] = true
				fmt.Fprintf(&sb, "- %s (/files/%s)\n", r.Slug, r.Slug)
				for _, snip := range r.Snippets {
					fmt.Fprintf(&sb, "  …%s…\n", snip)
				}
			}
		}
		if sb.Len() == 0 {
			return mcpText("No files found for: " + strings.Join(queries, ", "))
		}
		return mcpText(sb.String())
	}

	files, err := m.store.ListFiles()
	if err != nil {
		return mcpError("failed to list files: " + err.Error())
	}
	images, err := m.store.ListImages()
	if err != nil {
		return mcpError("failed to list images: " + err.Error())
	}
	entries := make([]mcpFileEntry, 0, len(files)+len(images))
	for _, f := range files {
		entries = append(entries, mcpFileEntry{Name: f.Name, URL: f.URL, Kind: "file", Size: f.Size, Modified: f.ModTime, UsedBy: f.UsedBy})
	}
	for _, f := range images {
		entries = append(entries, mcpFileEntry{Name: f.Name, URL: f.URL, Kind: "image", Size: f.Size, Modified: f.ModTime, UsedBy: f.UsedBy})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Modified.After(entries[j].Modified)
	})
	if len(entries) == 0 {
		return mcpText("No files uploaded.")
	}
	return mcpJSON(entries)
}

func (m *MCPHandler) toolGetFile(args map[string]any) mcpCallToolResult {
	name, ok := mcpArgString(args, "filename")
	if !ok {
		return mcpError("missing required argument: filename")
	}
	path, isImage, err := m.store.LocateUpload(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return mcpError("file not found: " + name)
		}
		return mcpError(err.Error())
	}
	ext := strings.ToLower(filepath.Ext(name))

	if isImage {
		data, err := os.ReadFile(path)
		if err != nil {
			return mcpError("failed to read file: " + err.Error())
		}
		if ext == ".svg" {
			return mcpText(string(data))
		}
		preview, mime, err := imagePreview(data, ext, mcpImageMaxPx, mcpImageMaxBytes)
		if err != nil {
			return mcpError("failed to read image: " + err.Error())
		}
		return mcpCallToolResult{Content: []mcpContent{{
			Type:     "image",
			Data:     base64.StdEncoding.EncodeToString(preview),
			MimeType: mime,
		}}}
	}

	info, err := os.Stat(path)
	if err != nil {
		return mcpError("failed to read file: " + err.Error())
	}
	header := fmt.Sprintf("File: %s (/files/%s, %d bytes, modified %s)", name, name, info.Size(), info.ModTime().Format(time.RFC3339))
	text, err := ExtractFileText(path)
	if err != nil {
		return mcpError("failed to extract text: " + err.Error())
	}
	if strings.TrimSpace(text) == "" {
		if ext == ".pdf" {
			return mcpText(header + "\n\nThis PDF has no text layer (it may be a scan).")
		}
		return mcpText(header + "\n\nNo text can be extracted from this file type.")
	}
	maxChars := 50000
	if n, ok := mcpArgNumber(args, "max_chars"); ok && n > 0 {
		maxChars = int(n)
	}
	text, cut := truncateText(text, maxChars)
	if cut {
		text += fmt.Sprintf("\n\n[truncated at %d characters]", maxChars)
	}
	return mcpText(header + "\n\n" + text)
}

func (m *MCPHandler) toolCreateUploadLink(args map[string]any, author string) mcpCallToolResult {
	if m.uploads == nil || m.externalURL == "" {
		return mcpError("upload links are not available on this server")
	}
	page, _ := mcpArgString(args, "page")
	page = strings.TrimSpace(page)
	if page != "" {
		if _, err := m.store.Load(KindPage, page); err != nil {
			return mcpError("page not found: " + page + " (use search_pages to find the exact slug)")
		}
	}
	token, expires := m.uploads.Create(page, author)
	msg := fmt.Sprintf("Upload link (single use, valid until %s UTC):\n%s/upload/%s\n\n", expires.UTC().Format("15:04"), m.externalURL, token)
	if page != "" {
		msg += fmt.Sprintf("Uploaded files will be added to the end of page '%s'.", page)
	} else {
		msg += "Uploaded files will be stored but not added to any page."
	}
	msg += "\n\nGive the user this link. When they say the upload is done, call list_files (newest first) to find the new file " +
		"and get_file to look at it or read its text. Embed an uploaded image in a page as ![alt](/images/<name>) " +
		"and link a document as [label](/files/<name>)."
	return mcpText(msg)
}

func (m *MCPHandler) toolDeleteFile(args map[string]any, author string) mcpCallToolResult {
	name, ok := mcpArgString(args, "filename")
	if !ok {
		return mcpError("missing required argument: filename")
	}
	path, isImage, err := m.store.LocateUpload(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return mcpError("file not found: " + name)
		}
		return mcpError(err.Error())
	}
	if err := os.Remove(path); err != nil {
		return mcpError("failed to delete file: " + err.Error())
	}
	if isImage {
		_ = m.autoCommit.CommitImageDelete(name, author)
	} else {
		_ = m.autoCommit.CommitFileDelete(name, author)
		m.store.RemoveFileIndex(name)
	}
	return mcpText(fmt.Sprintf("Deleted file '%s'", name))
}
