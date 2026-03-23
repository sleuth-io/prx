// Package imgrender downloads images, renders them as terminal graphics
// (sixel/kitty/iTerm2), and caches the results on disk.
package imgrender

import (
	"crypto/sha256"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/blacktop/go-termimg"
	"github.com/sleuth-io/prx/internal/dirs"
	_ "golang.org/x/image/webp"
)

// Supported returns true. Halfblock rendering works in any modern terminal.
func Supported() bool {
	return true
}

// Cache stores rendered terminal image strings keyed by URL hash.
type Cache struct {
	mu      sync.Mutex
	mem     map[string]string // url hash → rendered string
	dir     string
	maxCols int
	maxRows int
}

// NewCache creates a cache backed by ~/.cache/prx/images/.
// maxRows controls the thumbnail height in terminal rows.
func NewCache(maxCols, maxRows int) *Cache {
	dir, err := dirs.GetCacheDir()
	if err != nil {
		dir = filepath.Join(os.TempDir(), "prx")
	}
	dir = filepath.Join(dir, "images")
	_ = os.MkdirAll(dir, 0755)
	return &Cache{
		mem:     make(map[string]string),
		dir:     dir,
		maxCols: maxCols,
		maxRows: maxRows,
	}
}

func urlKey(url string) string {
	h := sha256.Sum256([]byte(url))
	return fmt.Sprintf("%x", h[:12])
}

// Get returns the cached rendered string for a URL, or "" if not cached.
func (c *Cache) Get(url string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.mem[urlKey(url)]; ok {
		return s
	}
	// Try disk
	key := urlKey(url)
	data, err := os.ReadFile(filepath.Join(c.dir, key))
	if err == nil && len(data) > 0 {
		s := string(data)
		c.mem[key] = s
		return s
	}
	return ""
}

// set stores a rendered string in memory and on disk.
func (c *Cache) set(url, rendered string) {
	key := urlKey(url)
	c.mu.Lock()
	c.mem[key] = rendered
	c.mu.Unlock()
	_ = os.WriteFile(filepath.Join(c.dir, key), []byte(rendered), 0644)
}

// FetchAndRender downloads an image URL, renders it for the terminal,
// and stores in cache. Returns the rendered string or error.
// This is meant to be called from a goroutine.
func (c *Cache) FetchAndRender(url string) (string, error) {
	if s := c.Get(url); s != "" {
		return s, nil
	}

	img, err := downloadImage(url)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}

	// Render a small thumbnail using auto-detected protocol (kitty/sixel/iterm2).
	rendered, err := termimg.New(img).
		Height(c.maxRows).
		Render()
	if err != nil {
		return "", fmt.Errorf("render: %w", err)
	}

	c.set(url, rendered)
	return rendered, nil
}

func downloadImage(url string) (image.Image, error) {
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	img, _, err := image.Decode(resp.Body)
	return img, err
}

// reImgTag matches <img> tags and extracts src and alt attributes.
var reImgTag = regexp.MustCompile(`<img\b[^>]*>`)
var reSrc = regexp.MustCompile(`src="([^"]*)"`)
var reAlt = regexp.MustCompile(`alt="([^"]*)"`)

// ImageRef is an image found in PR body HTML.
type ImageRef struct {
	URL string
	Alt string
}

// Placeholder returns a unique placeholder string for an image URL.
// This placeholder should be embedded in content that will be processed by lipgloss,
// then replaced with the actual image escape sequence afterward via InjectImages.
func Placeholder(url string) string {
	return "\x00IMG:" + urlKey(url) + "\x00"
}

// PlaceholderLines returns the number of blank lines the placeholder should occupy
// so there's room for the image.
func (c *Cache) PlaceholderLines() int {
	return c.maxRows
}

// InjectImages replaces all image placeholders in the rendered content with
// the actual terminal image escape sequences. Call this AFTER lipgloss rendering.
func (c *Cache) InjectImages(content string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, rendered := range c.mem {
		placeholder := "\x00IMG:" + key + "\x00"
		if idx := strings.Index(content, placeholder); idx >= 0 {
			content = strings.Replace(content, placeholder, strings.TrimRight(rendered, "\n"), 1)
		}
	}
	return content
}

// ExtractImages finds all <img> tags in HTML and returns their URLs and alt text.
func ExtractImages(html string) []ImageRef {
	var refs []ImageRef
	for _, match := range reImgTag.FindAllString(html, -1) {
		srcMatch := reSrc.FindStringSubmatch(match)
		if len(srcMatch) < 2 || srcMatch[1] == "" {
			continue
		}
		alt := ""
		if altMatch := reAlt.FindStringSubmatch(match); len(altMatch) >= 2 {
			alt = altMatch[1]
		}
		refs = append(refs, ImageRef{URL: srcMatch[1], Alt: alt})
	}
	return refs
}
