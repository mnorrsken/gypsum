package wiki

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mnorrsken/gypsum/web"
)

// testJPEG encodes a w×h JPEG whose left half is red, optionally with an
// EXIF block carrying the given orientation.
func testJPEG(t *testing.T, w, h, orientation int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{0, 0, 255, 255}
			if x < w/2 {
				c = color.RGBA{255, 0, 0, 255}
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	if orientation == 0 {
		return data
	}
	// Little-endian TIFF with one IFD0 entry: tag 0x0112, SHORT, count 1.
	tiff := []byte{'I', 'I', 0x2A, 0, 8, 0, 0, 0, 1, 0,
		0x12, 0x01, 3, 0, 1, 0, 0, 0, byte(orientation), 0, 0, 0,
		0, 0, 0, 0}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	segLen := len(payload) + 2
	app1 := append([]byte{0xFF, 0xE1, byte(segLen >> 8), byte(segLen)}, payload...)
	out := append([]byte{}, data[:2]...)
	out = append(out, app1...)
	return append(out, data[2:]...)
}

// testPDF builds a minimal one-page PDF showing text.
func testPDF(text string) []byte {
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		"",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	stream := fmt.Sprintf("BT /F1 12 Tf 20 100 Td (%s) Tj ET", text)
	objs[3] = fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return buf.Bytes()
}

func TestSaveUploadRejects(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))
	cfg := UploadConfig{MaxBytes: 1 << 20, ImageMaxPx: 2048}
	tests := []struct {
		name, filename string
		data           []byte
		want           string
	}{
		{"unknown type", "run.exe", []byte("MZ"), "unsupported file type"},
		{"heic", "IMG_0001.HEIC", []byte("x"), "HEIC"},
		{"content mismatch", "photo.png", testPDF("hi"), "does not match"},
		{"html as text", "page.txt", []byte("<html><script>alert(1)</script></html>"), "does not match"},
		{"empty", "notes.txt", nil, "empty"},
		{"too large", "big.txt", bytes.Repeat([]byte("a"), 2<<20), "larger than"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := store.SaveUpload(tt.filename, tt.data, cfg, "")
			var ue *UploadError
			if err == nil || !bytes.Contains([]byte(err.Error()), []byte(tt.want)) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.want)
			}
			if !errors.As(err, &ue) {
				t.Fatalf("err = %T, want *UploadError", err)
			}
		})
	}
}

func TestSaveUploadJPEGOrientsAndStripsEXIF(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))
	// 80×40 landscape stored with orientation 6 (rotate 90° clockwise).
	sf, err := store.SaveUpload("Holiday Photo.jpg", testJPEG(t, 80, 40, 6), UploadConfig{MaxBytes: 1 << 20, ImageMaxPx: 40}, "")
	if err != nil {
		t.Fatal(err)
	}
	if sf.Kind != "image" || !strings.HasPrefix(sf.Name, "holiday-photo-") || sf.Markdown != "![holiday photo]("+sf.URL+")" {
		t.Fatalf("unexpected stored file: %+v", sf)
	}
	data, err := os.ReadFile(filepath.Join(store.ImagesDir(), sf.Name))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("Exif")) {
		t.Error("stored JPEG still has an EXIF block")
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	// Scaled to 40×20, then rotated upright to 20×40.
	if b := img.Bounds(); b.Dx() != 20 || b.Dy() != 40 {
		t.Fatalf("size = %dx%d, want 20x40", b.Dx(), b.Dy())
	}
	// The red left half ends up on top after a clockwise turn.
	if r, _, bl, _ := img.At(10, 5).RGBA(); r < bl {
		t.Errorf("top of rotated image is not red")
	}
}

func TestSaveUploadPDFIsLinkedAndSearchable(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))
	sf, err := store.SaveUpload("Invoice.pdf", testPDF("Hello invoice 4711"), DefaultUploadConfig, "March invoice")
	if err != nil {
		t.Fatal(err)
	}
	if sf.Kind != "file" || sf.Markdown != "[March invoice](/files/"+sf.Name+")" {
		t.Fatalf("unexpected stored file: %+v", sf)
	}
	text, err := ExtractFileText(filepath.Join(store.FilesDir(), sf.Name))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "4711") {
		t.Fatalf("extracted text %q does not contain the PDF text", text)
	}
}

func TestFileSearchIndex(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewPageStore(filepath.Join(dir, "repo", "pages"))
	store.SetDB(db)

	sf, err := store.SaveUpload("receipt.pdf", testPDF("Plumber receipt kitchen"), DefaultUploadConfig, "")
	if err != nil {
		t.Fatal(err)
	}
	store.SyncFileIndex()
	results, err := store.SearchFiles("plumb")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Slug != sf.Name {
		t.Fatalf("search results = %+v, want %s", results, sf.Name)
	}

	_ = os.Remove(filepath.Join(store.FilesDir(), sf.Name))
	store.SyncFileIndex()
	if results, _ := store.SearchFiles("plumb"); len(results) != 0 {
		t.Fatalf("deleted file still found: %+v", results)
	}
}

// multipartBody builds a multipart form with one file and extra fields.
func multipartBody(t *testing.T, filename string, data []byte, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(data)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()
	return &buf, mw.FormDataContentType()
}

func TestFileUploadAndServe(t *testing.T) {
	_, routes := newTestHandler(t)

	body, ct := multipartBody(t, "scan.pdf", testPDF("total 12"), nil)
	req := httptest.NewRequest(http.MethodPost, "/files/upload", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: status %d, body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		OK    bool         `json:"ok"`
		Files []StoredFile `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || !resp.OK || len(resp.Files) != 1 {
		t.Fatalf("response = %s", rec.Body.String())
	}

	// The stored file is served back as a PDF.
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, resp.Files[0].URL, nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" {
		t.Fatalf("serve: status %d, content type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestUploadLinkSingleUse(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))
	h := NewHandler(store, NewMarkdownRenderer(), web.Templates(), nil, nil, nil, AllMCPSections)
	routes := h.Routes()
	_ = store.Save(KindPage, "Garden", "# Garden\n")
	token, _ := h.uploads.Create("Garden", "test (MCP)")

	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/upload/"+token, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `name="file"`) {
		t.Fatalf("GET: status %d", rec.Code)
	}

	post := func() *httptest.ResponseRecorder {
		body, ct := multipartBody(t, "tomato.jpg", testJPEG(t, 16, 16, 0), map[string]string{"caption": "Tomatoes"})
		req := httptest.NewRequest(http.MethodPost, "/upload/"+token, body)
		req.Header.Set("Content-Type", ct)
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, req)
		return rec
	}
	if rec := post(); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Saved 1 file") {
		t.Fatalf("first POST: status %d\n%s", rec.Code, rec.Body.String())
	}
	page, _ := store.Load(KindPage, "Garden")
	if !strings.Contains(page.Content, "![Tomatoes](/images/tomato-") {
		t.Fatalf("page does not embed the photo:\n%s", page.Content)
	}
	if rec := post(); rec.Code != http.StatusNotFound {
		t.Fatalf("second POST: status %d, want 404", rec.Code)
	}
}

func TestMCPFileTools(t *testing.T) {
	handler, store := newTestMCP(t)
	handler.SetUploadLinks(NewUploadLinks(), "https://wiki.example.com/")
	_ = store.Save(KindPage, "Home", "# Home\n")

	img, err := store.SaveUpload("cat.jpg", testJPEG(t, 32, 16, 0), DefaultUploadConfig, "")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := store.SaveUpload("notes.txt", []byte("buy milk"), DefaultUploadConfig, "")
	if err != nil {
		t.Fatal(err)
	}

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{"name": "get_file", "arguments": map[string]any{"filename": img.Name}})
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), `"type":"image"`) || strings.Contains(string(raw), `"text"`) {
		t.Fatalf("get_file image result = %s", raw)
	}
	var result mcpCallToolResult
	_ = json.Unmarshal(raw, &result)
	data, err := base64.StdEncoding.DecodeString(result.Content[0].Data)
	if err != nil || result.Content[0].MimeType != "image/jpeg" {
		t.Fatalf("image block: mime %q, decode err %v", result.Content[0].MimeType, err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("returned image does not decode: %v", err)
	}

	text := toolResultText(t, mcpCall(t, handler, 2, "tools/call", map[string]any{"name": "get_file", "arguments": map[string]any{"filename": doc.Name}}))
	if !strings.Contains(text, "buy milk") {
		t.Fatalf("get_file text = %q", text)
	}

	text = toolResultText(t, mcpCall(t, handler, 3, "tools/call", map[string]any{"name": "create_upload_link", "arguments": map[string]any{"page": "Home"}}))
	if !strings.Contains(text, "https://wiki.example.com/upload/") {
		t.Fatalf("create_upload_link = %q", text)
	}
	resp = mcpCall(t, handler, 4, "tools/call", map[string]any{"name": "create_upload_link", "arguments": map[string]any{"page": "Nope"}})
	_ = json.Unmarshal(mustJSON(resp.Result), &result)
	if !result.IsError {
		t.Fatal("create_upload_link accepted a missing page")
	}

	// The image is newer than the document, so it is listed first.
	older := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(store.FilesDir(), doc.Name), older, older)
	text = toolResultText(t, mcpCall(t, handler, 5, "tools/call", map[string]any{"name": "list_files", "arguments": map[string]any{}}))
	if i, j := strings.Index(text, img.Name), strings.Index(text, doc.Name); i < 0 || j < 0 || i > j {
		t.Fatalf("list_files is not newest first: %s", text)
	}

	toolResultText(t, mcpCall(t, handler, 6, "tools/call", map[string]any{"name": "delete_file", "arguments": map[string]any{"filename": doc.Name}}))
	if _, err := os.Stat(filepath.Join(store.FilesDir(), doc.Name)); !os.IsNotExist(err) {
		t.Fatalf("file still exists after delete_file: %v", err)
	}
	resp = mcpCall(t, handler, 7, "tools/call", map[string]any{"name": "get_file", "arguments": map[string]any{"filename": "../secret"}})
	_ = json.Unmarshal(mustJSON(resp.Result), &result)
	if !result.IsError {
		t.Fatal("get_file accepted a path outside the upload directories")
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
