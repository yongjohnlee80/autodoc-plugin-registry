package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	regindex "github.com/yongjohnlee80/autodoc/core/registry"
)

// BuildIndex is the index of entries, in their order, with each one's declarations. generated is
// when: the registry's own commit time, never the clock, so the same entries give the same bytes.
func BuildIndex(entries []Entry, declares map[string]regindex.Declares, generated time.Time) regindex.Index {
	ix := regindex.Index{Format: regindex.Format, Generated: generated.UTC().Format(time.RFC3339), Entries: []regindex.IndexEntry{}}
	for _, e := range entries {
		d := declares[e.Kind+"/"+e.Name]
		if d.Items == nil {
			d.Items = []string{}
		}
		ie := regindex.IndexEntry{Kind: e.Kind, Name: e.Name, Title: e.Title, Description: e.Description,
			Repo: e.Repo, Author: e.Author, Declares: d, Audits: []regindex.IndexAudit{}}
		for _, a := range newestFirst(e.Audits) {
			ie.Audits = append(ie.Audits, regindex.IndexAudit{Commit: a.Commit, Model: a.Model,
				Date: a.Date.UTC().Format(time.DateOnly), By: a.By, Report: regindex.PagesURL + a.Report})
		}
		ix.Entries = append(ix.Entries, ie)
	}
	return ix
}

// IndexJSON is the index's bytes: indented, with a final newline.
func IndexJSON(ix regindex.Index) ([]byte, error) {
	b, err := json.MarshalIndent(ix, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// WriteSite writes the site into out: index.json, a page listing every entry, one page per entry,
// and the audit reports, copied from root (the registry's root), at the paths the index names. An
// index AutoDoc would refuse (regindex.Validate, the client's own check) is never written.
func WriteSite(out string, root fs.FS, ix regindex.Index) error {
	if err := regindex.Validate(ix); err != nil {
		return err
	}
	b, err := IndexJSON(ix)
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(out, "index.json"), b); err != nil {
		return err
	}
	var page bytes.Buffer
	if err := listTmpl.Execute(&page, ix); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(out, "index.html"), page.Bytes()); err != nil {
		return err
	}
	for _, e := range ix.Entries {
		page.Reset()
		if err := entryTmpl.Execute(&page, e); err != nil {
			return err
		}
		if err := writeFile(filepath.Join(out, e.Kind, e.Name, "index.html"), page.Bytes()); err != nil {
			return err
		}
		for _, a := range e.Audits {
			rel := regindex.ReportPath(e.Kind, e.Name, a.Commit)
			src, err := fs.ReadFile(root, rel)
			if err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			if err := writeFile(filepath.Join(out, filepath.FromSlash(rel)), src); err != nil {
				return err
			}
		}
	}
	return writeFile(filepath.Join(out, ".nojekyll"), nil)
}

func writeFile(p string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}

const style = `body{font:16px/1.5 system-ui,sans-serif;max-width:52rem;margin:2rem auto;padding:0 1rem;color:#1d1d1f;background:#fff}
@media (prefers-color-scheme:dark){body{color:#e8e8ea;background:#141416}a{color:#8ab4ff}}
code{font-size:.9em}.audited{color:#1a7f37}.unaudited{color:#9a6700}li{margin:.3rem 0}`

var listTmpl = template.Must(template.New("list").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>AutoDoc registry</title><style>` + style + `</style></head><body>
<h1>AutoDoc registry</h1>
<p>Plugins, Lua packages and KB types AutoDoc can install by name. An audit is an AI model's reading
of the code at one commit: advice, not a guarantee. AutoDoc always shows its risk warning.
The machine-readable list is <a href="index.json">index.json</a>.</p>
<ul>{{range .Entries}}<li><a href="{{.Kind}}/{{.Name}}/">{{.Title}}</a> <small>({{.Kind}})</small>,
{{.Description}} {{if .Audits}}<span class="audited">audited</span>{{else}}<span class="unaudited">not audited</span>{{end}}</li>
{{end}}</ul></body></html>
`))

var entryTmpl = template.Must(template.New("entry").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}} · AutoDoc registry</title><style>` + style + `</style></head><body>
<p><a href="../../">AutoDoc registry</a></p>
<h1>{{.Title}}</h1>
<p>{{.Description}}</p>
<p>A {{.Kind}}, <code>{{.Name}}</code>, by {{.Author}}: <a href="{{.Repo}}">{{.Repo}}</a></p>
<h2>What it declares</h2>
<p>At commit <code>{{.Declares.Commit}}</code>:</p>
<ul>{{range .Declares.Items}}<li>{{.}}</li>{{else}}<li>nothing beyond its dialog</li>{{end}}</ul>
<h2>Audits</h2>
{{if .Audits}}<ul>{{range .Audits}}<li>{{.Date}}: <code>{{.Commit}}</code>, read by {{.Model}} for {{.By}}
(<a href="{{.Report}}">report</a>)</li>{{end}}</ul>{{else}}<p class="unaudited">Not audited at any commit.</p>{{end}}
</body></html>
`))
