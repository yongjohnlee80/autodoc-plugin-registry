// Package registry reads the registry's entries, checks them, and builds the index AutoDoc fetches
// (ADR 1792351195). An entry is one TOML file, entries/<kind dir>/<name>.toml, naming a plugin, a
// Lua package or a KB type by its git repo, with the audits made of it, each at a full commit.
//
// What an entry declares it does is never written on the entry: it is read from the repo's own
// manifest, at a commit, by AutoDoc's own readers (core/manifests, core/kbtype), so the registry and
// AutoDoc never disagree on what a manifest means.
package registry

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	regindex "github.com/yongjohnlee80/autodoc/core/registry"
)

// The kinds an entry may be are AutoDoc's (core/registry owns the index format and its rules); this
// is the directory under entries/ each lives in.
var kindDirs = map[string]string{regindex.KindPlugin: "plugins", regindex.KindLua: "lua", regindex.KindKBType: "kb-types"}

// Entry is one entries/<kind dir>/<name>.toml.
type Entry struct {
	Kind        string  `toml:"kind"`
	Name        string  `toml:"name"`
	Title       string  `toml:"title"`
	Description string  `toml:"description"`
	Repo        string  `toml:"repo"`
	Author      string  `toml:"author"`
	Audits      []Audit `toml:"audit"`

	// File is the entry's path in the registry, entries/<kind dir>/<name>.toml.
	File string `toml:"-"`
}

// Audit is one [[audit]]: a model's reading of the repo at Commit, by By, on Date, its report at
// Report (audits/<kind>/<name>/<commit>.md).
type Audit struct {
	Commit string    `toml:"commit"`
	Model  string    `toml:"model"`
	Date   time.Time `toml:"date"`
	By     string    `toml:"by"`
	Report string    `toml:"report"`
}

// Load reads every entry under entries/ of fsys, the registry's root. A file that does not parse,
// or names a key the schema does not have, is an error naming the file; the entries are in kind
// then name order.
func Load(fsys fs.FS) ([]Entry, error) {
	var out []Entry
	var errs []error
	for kind, dir := range kindDirs {
		files, err := fs.Glob(fsys, path.Join("entries", dir, "*.toml"))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			src, err := fs.ReadFile(fsys, f)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", f, err))
				continue
			}
			var e Entry
			md, err := toml.NewDecoder(bytes.NewReader(src)).Decode(&e)
			switch {
			case err != nil:
				errs = append(errs, fmt.Errorf("%s: %v", f, err))
				continue
			case len(md.Undecoded()) > 0:
				errs = append(errs, fmt.Errorf("%s: unknown key %q", f, md.Undecoded()[0].String()))
				continue
			}
			e.File = f
			if e.Kind != kind {
				errs = append(errs, fmt.Errorf("%s: kind %q, but it is under entries/%s/, the %s entries", f, e.Kind, dir, kind))
				continue
			}
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b Entry) int {
		if c := strings.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out, errors.Join(errs...)
}

// Validate checks what an entry says of itself, its audits' reports included, against fsys, the
// registry's root: the file named for the entry, the fields set, the repo an https URL on an
// allowed host, and each audit a full commit with its report in place. What the repo holds is
// checked by Verify.
func Validate(fsys fs.FS, entries []Entry) error {
	var errs []error
	seen := map[string]string{}
	for _, e := range entries {
		bad := func(format string, args ...any) {
			errs = append(errs, fmt.Errorf("%s: "+format, append([]any{e.File}, args...)...))
		}
		want := path.Join("entries", kindDirs[e.Kind], e.Name+".toml")
		switch {
		case e.Name == "" || strings.ContainsAny(e.Name, `/\`) || e.Name != strings.TrimSpace(e.Name):
			bad("name %q: want the manifest's name", e.Name)
		case e.File != want:
			bad("the file is named for %q, but the entry is %q", path.Base(e.File), e.Name)
		}
		if first, dup := seen[e.Kind+"/"+e.Name]; dup {
			bad("%s %q is %s's already", e.Kind, e.Name, first)
		}
		seen[e.Kind+"/"+e.Name] = e.File
		for field, v := range map[string]string{"title": e.Title, "description": e.Description, "author": e.Author} {
			if strings.TrimSpace(v) == "" {
				bad("no %s", field)
			}
		}
		if err := regindex.CheckRepo(e.Repo); err != nil {
			bad("repo %q: %v", e.Repo, err)
		}
		commits := map[string]bool{}
		for i, a := range e.Audits {
			switch {
			case !regindex.IsFullSHA(a.Commit):
				bad("audit %d: commit %q: want the full 40-character SHA (a tag can move; a commit can't)", i+1, a.Commit)
				continue
			case commits[a.Commit]:
				bad("audit %d: commit %s is audited twice", i+1, a.Commit)
			case strings.TrimSpace(a.Model) == "" || strings.TrimSpace(a.By) == "":
				bad("audit %d: model and by are both required", i+1)
			case a.Date.IsZero():
				bad("audit %d: no date", i+1)
			}
			commits[a.Commit] = true
			if want := regindex.ReportPath(e.Kind, e.Name, a.Commit); a.Report != want {
				bad("audit %d: report %q: want %q", i+1, a.Report, want)
			} else if _, err := fs.Stat(fsys, want); err != nil {
				bad("audit %d: report %s is missing", i+1, want)
			}
		}
	}
	return errors.Join(errs...)
}
