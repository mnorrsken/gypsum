package wiki

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// Site-image fetching: when a secret has a URL but no image, the server fetches
// a representative picture for the site so the vault reads like a list of
// recognisable services rather than a wall of text.
//
// This is a metadata fetch, not a rendered screenshot — Gypsum has no headless
// browser. In practice og:image is what a site itself nominates as its picture,
// which is closer to "what the site looks like" than a 16px favicon. Candidates
// are tried in descending order of usefulness and the first that decodes as an
// image wins:
//
//	og:image → twitter:image → apple-touch-icon → <link rel=icon> → /favicon.ico
//
// The request is made by the server, on behalf of a logged-in user who typed
// the URL, so it can reach hosts the browser can (including intranet hosts on
// a self-hosted deployment). Nothing is sent to any third-party service.

const (
	// siteImageMaxHTML caps the HTML read while looking for image tags.
	siteImageMaxHTML = 1 << 20 // 1 MiB
	// siteImageMaxBytes caps the downloaded image itself.
	siteImageMaxBytes = 5 << 20 // 5 MiB
	// siteImageUserAgent identifies the fetcher to the remote site.
	siteImageUserAgent = "Gypsum-Wiki/1.0 (+site-image-fetch)"
)

// siteImageExt maps an image content type to the file extension it is stored
// under. A type absent from this map is not accepted.
var siteImageExt = map[string]string{
	"image/png":                ".png",
	"image/jpeg":               ".jpg",
	"image/jpg":                ".jpg",
	"image/gif":                ".gif",
	"image/webp":               ".webp",
	"image/svg+xml":            ".svg",
	"image/avif":               ".avif",
	"image/x-icon":             ".ico",
	"image/vnd.microsoft.icon": ".ico",
	"image/ico":                ".ico",
}

// SiteImage is a downloaded site image ready to be written to the images dir.
type SiteImage struct {
	Data []byte
	Ext  string // file extension including the leading dot
}

// FetchSiteImage downloads a representative image for pageURL. It returns an
// error when the URL is unusable, the page cannot be read, or no candidate
// yields a decodable image — callers fall back to the mnemonic tile.
func FetchSiteImage(ctx context.Context, client *http.Client, pageURL string) (*SiteImage, error) {
	base, err := url.Parse(strings.TrimSpace(pageURL))
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("unsupported url scheme %q", base.Scheme)
	}
	if base.Host == "" {
		return nil, fmt.Errorf("url has no host")
	}
	if client == nil {
		client = http.DefaultClient
	}

	candidates := siteImageCandidates(ctx, client, base)
	// /favicon.ico is the last resort and always worth a try.
	candidates = append(candidates, base.ResolveReference(&url.URL{Path: "/favicon.ico"}).String())

	var lastErr error
	seen := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		img, err := downloadSiteImage(ctx, client, candidate)
		if err != nil {
			lastErr = err
			continue
		}
		return img, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no image found for %s", base.Host)
	}
	return nil, lastErr
}

// siteImageCandidates fetches pageURL and returns absolute image URLs
// advertised in its <head>, best first. An unreachable or non-HTML page yields
// no candidates rather than an error — /favicon.ico may still work.
func siteImageCandidates(ctx context.Context, client *http.Client, base *url.URL) []string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", siteImageUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(strings.ToLower(ct), "html") {
		return nil
	}
	// The response URL is the base for relative links: it follows redirects.
	if resp.Request != nil && resp.Request.URL != nil {
		base = resp.Request.URL
	}
	return parseSiteImageLinks(io.LimitReader(resp.Body, siteImageMaxHTML), base)
}

// parseSiteImageLinks extracts image candidates from an HTML document in
// priority order: og:image, twitter:image, apple-touch-icon, then any
// rel="icon" link. Relative references are resolved against base.
func parseSiteImageLinks(r io.Reader, base *url.URL) []string {
	var og, twitter, apple, icon string

	tokenizer := html.NewTokenizer(r)
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return compactStrings(og, twitter, apple, icon)
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			switch token.Data {
			case "meta":
				key := strings.ToLower(attr(token, "property"))
				if key == "" {
					key = strings.ToLower(attr(token, "name"))
				}
				content := resolveRef(base, attr(token, "content"))
				if content == "" {
					continue
				}
				switch key {
				case "og:image", "og:image:url", "og:image:secure_url":
					if og == "" {
						og = content
					}
				case "twitter:image", "twitter:image:src":
					if twitter == "" {
						twitter = content
					}
				}
			case "link":
				rels := strings.Fields(strings.ToLower(attr(token, "rel")))
				href := resolveRef(base, attr(token, "href"))
				if href == "" {
					continue
				}
				for _, rel := range rels {
					switch rel {
					case "apple-touch-icon", "apple-touch-icon-precomposed":
						if apple == "" {
							apple = href
						}
					case "icon", "shortcut":
						if icon == "" {
							icon = href
						}
					}
				}
			case "body":
				// Everything we look for lives in <head>.
				return compactStrings(og, twitter, apple, icon)
			}
		}
	}
}

// attr returns the value of the named attribute, or "" when absent.
func attr(t html.Token, name string) string {
	for _, a := range t.Attr {
		if strings.EqualFold(a.Key, name) {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}

// resolveRef makes ref absolute against base, returning "" if it is unusable.
func resolveRef(base *url.URL, ref string) string {
	if ref == "" || strings.HasPrefix(ref, "data:") {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	abs := base.ResolveReference(u)
	if abs.Scheme != "http" && abs.Scheme != "https" {
		return ""
	}
	return abs.String()
}

// compactStrings drops empty entries, preserving order.
func compactStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// downloadSiteImage fetches one candidate URL and validates that it really is
// an image of an accepted type and a sane size.
func downloadSiteImage(ctx context.Context, client *http.Client, imageURL string) (*SiteImage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", siteImageUserAgent)
	req.Header.Set("Accept", "image/*")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: status %d", imageURL, resp.StatusCode)
	}

	// Read one byte past the cap so an oversized image is detected rather than
	// silently truncated into a corrupt file.
	data, err := io.ReadAll(io.LimitReader(resp.Body, siteImageMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%s: empty response", imageURL)
	}
	if len(data) > siteImageMaxBytes {
		return nil, fmt.Errorf("%s: image larger than %d bytes", imageURL, siteImageMaxBytes)
	}

	ext, ok := siteImageExt[normalizeContentType(resp.Header.Get("Content-Type"))]
	if !ok {
		// Servers routinely mislabel favicons as text/plain or
		// application/octet-stream; fall back to sniffing the bytes.
		ext, ok = siteImageExt[normalizeContentType(http.DetectContentType(data))]
	}
	if !ok {
		return nil, fmt.Errorf("%s: not an accepted image type", imageURL)
	}
	return &SiteImage{Data: data, Ext: ext}, nil
}

// normalizeContentType lowercases a Content-Type and strips its parameters.
func normalizeContentType(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}
