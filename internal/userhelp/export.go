package userhelp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/ivantit66/onebase/internal/fsmode"
	oblog "github.com/ivantit66/onebase/internal/logging"
)

// Bundle is a transport-neutral publication contract, usable by REST adapters,
// MCP tools or other documentation portals. Images are embedded for portability.
type Bundle struct {
	Version int           `json:"version"`
	Title   string        `json:"title"`
	Pages   []BundlePage  `json:"pages"`
	Assets  []BundleAsset `json:"assets"`
}

type BundlePage struct {
	ChapterSpec
	Markdown string `json:"markdown"`
	HTML     string `json:"html"`
}

type BundleAsset struct {
	Path   string `json:"path"`
	MIME   string `json:"mime"`
	Base64 string `json:"base64"`
}

// Export writes a self-contained HTML website with local search and a portable
// bundle.json. The output must be a separate directory, not docs/help itself.
func (b *Book) Export(dir string) error {
	if !b.Available() {
		return fmt.Errorf("user documentation is disabled or empty")
	}
	// Check all chapter names before touching the output, including on hosts
	// with case-sensitive filesystems: the exported site must be portable.
	ids := make(map[string]string, len(b.Chapters))
	for _, c := range b.Chapters {
		key := strings.ToLower(c.ID)
		if previous, exists := ids[key]; exists {
			return fmt.Errorf("chapter IDs %q and %q collide in a case-insensitive HTML export; rename one of them", previous, c.ID)
		}
		ids[key] = c.ID
	}
	if err := os.MkdirAll(dir, fsmode.Dir); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer oblog.CloseQuiet("userhelp", "export root", root)
	write := func(name string, data []byte) error {
		if err := root.MkdirAll(path.Dir(name), fsmode.Dir); err != nil {
			return err
		}
		return root.WriteFile(name, data, fsmode.File)
	}
	chapterURL := func(id string) string { return "chapter-" + id + ".html" }
	assetURL := func(p string) string { return "assets/" + (&url.URL{Path: p}).EscapedPath() }
	bundle := Bundle{Version: 1, Title: b.Title}
	var index strings.Builder
	index.WriteString("<ul id=chapters>")
	for i := range b.Chapters {
		c := &b.Chapters[i]
		body, err := b.Render(c, chapterURL, assetURL)
		if err != nil {
			return err
		}
		bundle.Pages = append(bundle.Pages, BundlePage{ChapterSpec: c.ChapterSpec, Markdown: c.Markdown, HTML: body})
		link := html.EscapeString(chapterURL(c.ID))
		fmt.Fprintf(&index, `<li data-search="%s"><a href="%s">%s</a></li>`, html.EscapeString(strings.ToLower(c.Title+" "+c.Markdown)), link, html.EscapeString(c.Title))
		page := standalone(c.Title, `<a href="index.html">`+html.EscapeString(b.Title)+`</a>`+body)
		if err := write(chapterURL(c.ID), []byte(page)); err != nil {
			return err
		}
	}
	index.WriteString("</ul><script>document.querySelector('input').addEventListener('input',function(){var words=this.value.toLowerCase().split(/\\s+/).filter(Boolean);document.querySelectorAll('#chapters li').forEach(function(li){li.hidden=!words.every(function(w){return li.dataset.search.indexOf(w)>=0})})});</script>")
	if err := write("index.html", []byte(standalone(b.Title, "<h1>"+html.EscapeString(b.Title)+`</h1><input type="search" aria-label="Search" placeholder="Search">`+index.String()))); err != nil {
		return err
	}
	for _, p := range b.assetPaths() {
		a := b.Assets[p]
		if err := write("assets/"+p, a.Data); err != nil {
			return err
		}
		bundle.Assets = append(bundle.Assets, BundleAsset{Path: p, MIME: a.MIME, Base64: base64.StdEncoding.EncodeToString(a.Data)})
	}
	raw, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	return write("bundle.json", append(raw, '\n'))
}

func standalone(title, body string) string {
	return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` + html.EscapeString(title) + `</title><style>body{font:16px/1.6 system-ui,sans-serif;max-width:1000px;margin:2rem auto;padding:0 1rem;color:#1e293b}img{max-width:100%;height:auto}pre{overflow:auto;background:#f1f5f9;padding:1rem}table{border-collapse:collapse}td,th{border:1px solid #cbd5e1;padding:.4rem}input{padding:.5rem;width:90%}a{color:#1d4ed8}</style></head><body>` + body + `</body></html>`
}
