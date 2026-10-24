// Package userhelp implements optional, project-owned user documentation.
// A book is loaded into memory so database-backed projects and hot reloads do
// not depend on the lifetime of their temporary source directory.
package userhelp

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"

	oblog "github.com/ivantit66/onebase/internal/logging"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"gopkg.in/yaml.v3"
)

const Directory = "docs/help"

type Manifest struct {
	Title      string        `yaml:"title" json:"title"`
	Enabled    *bool         `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	AutoUpdate *bool         `yaml:"auto_update,omitempty" json:"auto_update,omitempty"`
	Chapters   []ChapterSpec `yaml:"chapters" json:"chapters"`
}

// Chapters appear in manifest order. Parent must refer to an earlier chapter.
// Objects use kind/name; forms use kind/name/form-name (case insensitive).
type ChapterSpec struct {
	ID      string   `yaml:"id" json:"id"`
	Title   string   `yaml:"title" json:"title"`
	File    string   `yaml:"file" json:"file"`
	Parent  string   `yaml:"parent,omitempty" json:"parent,omitempty"`
	Objects []string `yaml:"objects,omitempty" json:"objects,omitempty"`
	Forms   []string `yaml:"forms,omitempty" json:"forms,omitempty"`
}

type Chapter struct {
	ChapterSpec
	Markdown string `json:"markdown"`
	Depth    int    `json:"depth"`
}

type Asset struct {
	Data []byte
	MIME string
}

// Book is immutable after Load. Callers must not change its slices or maps.
type Book struct {
	Title      string
	Enabled    bool
	AutoUpdate bool
	Chapters   []Chapter
	Assets     map[string]Asset
	Warnings   []string
}

var chapterID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

var imageTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp",
}

func emptyBook() *Book {
	return &Book{Enabled: true, AutoUpdate: true, Assets: map[string]Asset{}}
}

// Load never returns a fatal error: missing documentation is normal; malformed
// documentation produces warnings and leaves application startup unaffected.
func Load(projectDir string) *Book {
	b := emptyBook()
	root, err := os.OpenRoot(projectDir)
	if err != nil {
		b.warn("%v", err)
		return b
	}
	defer oblog.CloseQuiet("userhelp", "source root", root)
	raw, err := readLimited(root, Directory+"/index.yaml", 1<<20)
	if os.IsNotExist(err) {
		return b
	}
	if err != nil {
		b.warn("index.yaml: %v", err)
		return b
	}
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		b.warn("index.yaml: %v", err)
		return b
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		b.warn("index.yaml: expected one YAML document")
		return b
	}
	b.Title = m.Title
	if b.Title == "" {
		b.Title = "Руководство пользователя"
	}
	if m.Enabled != nil {
		b.Enabled = *m.Enabled
	}
	if m.AutoUpdate != nil {
		b.AutoUpdate = *m.AutoUpdate
	}
	if !b.Enabled {
		return b
	}
	ids, files := map[string]bool{}, map[string]bool{}
	for _, spec := range m.Chapters {
		if !chapterID.MatchString(spec.ID) || ids[spec.ID] || spec.Title == "" || !safePath(spec.File) || path.Ext(spec.File) != ".md" || files[spec.File] {
			b.warn("invalid or duplicate chapter: %q (%s)", spec.ID, spec.File)
			continue
		}
		depth := 0
		if spec.Parent != "" {
			parent := b.Find(spec.Parent)
			if parent == nil {
				b.warn("%s: parent %q must precede its child", spec.ID, spec.Parent)
				continue
			}
			depth = parent.Depth + 1
		}
		content, err := readLimited(root, Directory+"/"+spec.File, 2<<20)
		if err != nil {
			b.warn("%s: %v", spec.File, err)
			continue
		}
		ids[spec.ID], files[spec.File] = true, true
		b.Chapters = append(b.Chapters, Chapter{ChapterSpec: spec, Markdown: string(content), Depth: depth})
	}
	// Load referenced images only; unrelated files (including development notes)
	// are neither served nor published. os.Root confines reads through symlinks.
	var total int
	for i := range b.Chapters {
		c := &b.Chapters[i]
		src := []byte(c.Markdown)
		doc := markdown().Parser().Parse(text.NewReader(src))
		_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if !entering {
				return ast.WalkContinue, nil
			}
			switch n := n.(type) {
			case *ast.Image:
				asset, _, ok := localPath(c.File, string(n.Destination))
				mime := imageTypes[strings.ToLower(path.Ext(asset))]
				if !ok || mime == "" {
					b.warn("%s: image must be a local PNG, JPEG, GIF or WebP: %s", c.File, n.Destination)
					break
				}
				if _, exists := b.Assets[asset]; exists {
					break
				}
				data, err := readLimited(root, Directory+"/"+asset, 8<<20)
				if err != nil {
					b.warn("%s: image %s: %v", c.File, asset, err)
					break
				}
				total += len(data)
				if total > 64<<20 {
					b.warn("%s: image budget exceeded (64 MiB)", c.File)
					break
				}
				b.Assets[asset] = Asset{Data: data, MIME: mime}
			case *ast.Link:
				p, _, ok := localPath(c.File, string(n.Destination))
				if ok && path.Ext(p) == ".md" && b.byFile(p) == nil {
					b.warn("%s: missing chapter link: %s", c.File, n.Destination)
				}
			}
			return ast.WalkContinue, nil
		})
	}
	return b
}

func readLimited(root *os.Root, name string, limit int64) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer oblog.CloseQuiet("userhelp", "source file", f)
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return b, err
}

func safePath(p string) bool {
	return fs.ValidPath(p) && p != "." && !strings.ContainsAny(p, `\:*?"<>|`)
}

func localPath(from, dest string) (string, string, bool) {
	u, err := url.Parse(dest)
	if err != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" || strings.HasPrefix(u.Path, "/") || u.Path == "" {
		return "", "", false
	}
	p := path.Join(path.Dir(from), u.Path)
	if !safePath(p) {
		return "", "", false
	}
	fragment := ""
	if u.Fragment != "" {
		fragment = "#" + url.PathEscape(u.Fragment)
	}
	return p, fragment, true
}

func (b *Book) warn(format string, args ...any) {
	b.Warnings = append(b.Warnings, fmt.Sprintf(format, args...))
}

func (b *Book) Available() bool { return b != nil && b.Enabled && len(b.Chapters) > 0 }

func (b *Book) Find(id string) *Chapter {
	if b != nil {
		for i := range b.Chapters {
			if b.Chapters[i].ID == id {
				return &b.Chapters[i]
			}
		}
	}
	return nil
}

func (b *Book) byFile(file string) *Chapter {
	for i := range b.Chapters {
		if b.Chapters[i].File == file {
			return &b.Chapters[i]
		}
	}
	return nil
}

func (b *Book) Context(object, form string) *Chapter {
	if !b.Available() {
		return nil
	}
	if form != "" {
		for i := range b.Chapters {
			for _, binding := range b.Chapters[i].Forms {
				if strings.EqualFold(binding, object+"/"+form) {
					return &b.Chapters[i]
				}
			}
		}
	}
	for i := range b.Chapters {
		for _, binding := range b.Chapters[i].Objects {
			if strings.EqualFold(binding, object) {
				return &b.Chapters[i]
			}
		}
	}
	return nil
}

func (b *Book) Search(query string) []Chapter {
	var out []Chapter
	if b.Available() {
		words := strings.Fields(strings.ToLower(query))
		for _, c := range b.Chapters {
			haystack, matches := strings.ToLower(c.Title+"\n"+c.Markdown), true
			for _, word := range words {
				matches = matches && strings.Contains(haystack, word)
			}
			if matches {
				out = append(out, c)
			}
		}
	}
	return out
}

func markdown() goldmark.Markdown {
	// XHTML is also valid browser HTML, and keeps the whole rendered fragment
	// well-formed when Publish embeds it in Confluence storage XML.
	return goldmark.New(
		goldmark.WithExtensions(extension.Table, extension.Strikethrough),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithXHTML()),
	)
}

// Render rewrites relative links for the selected delivery channel. Raw HTML
// and dangerous URL schemes remain disabled by the Markdown renderer.
func (b *Book) Render(c *Chapter, chapterURL, assetURL func(string) string) (string, error) {
	md := markdown()
	src := []byte(c.Markdown)
	doc := md.Parser().Parse(text.NewReader(src))
	if err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			switch n := n.(type) {
			case *ast.Image:
				p, _, ok := localPath(c.File, string(n.Destination))
				if _, found := b.Assets[p]; ok && found {
					n.Destination = []byte(assetURL(p))
				} else {
					n.Destination = nil
				}
			case *ast.Link:
				if p, fragment, ok := localPath(c.File, string(n.Destination)); ok {
					if target := b.byFile(p); target != nil {
						n.Destination = []byte(chapterURL(target.ID) + fragment)
					} else {
						n.Destination = nil
					}
				} else if dest := string(n.Destination); !strings.HasPrefix(dest, "#") {
					u, err := url.Parse(dest)
					if err != nil || (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "mailto") {
						n.Destination = nil
					}
				}
			}
		}
		return ast.WalkContinue, nil
	}); err != nil {
		return "", err
	}
	var out bytes.Buffer
	err := md.Renderer().Render(&out, src, doc)
	return out.String(), err
}
