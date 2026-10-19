package registry

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/yongjohnlee80/autodoc/core/kbtype"
	"github.com/yongjohnlee80/autodoc/core/manifests"
	regindex "github.com/yongjohnlee80/autodoc/core/registry"
)

// Read reads the manifest of e's kind in tree, a checkout of its repo, through AutoDoc's own readers,
// and says what it declares. The manifest must parse, be valid, and name the entry.
func Read(e Entry, tree string) ([]string, error) {
	fsys := os.DirFS(tree)
	switch e.Kind {
	case regindex.KindPlugin:
		m, err := manifests.ReadPlugin(fsys)
		if err != nil {
			return nil, err
		}
		if m.Name != e.Name {
			return nil, fmt.Errorf("plugin.toml names %q, not %q", m.Name, e.Name)
		}
		items := m.Declares()
		lua, err := fs.Sub(fsys, "lua")
		if err != nil {
			return nil, err
		}
		if _, err := fs.Stat(lua, "package.toml"); err == nil {
			p, err := manifests.ReadPluginPackage(lua, e.Name)
			if err != nil {
				return nil, fmt.Errorf("lua/%w", err)
			}
			items = append(items, luaItem(p))
		}
		return items, nil
	case regindex.KindLua:
		p, err := manifests.ReadPackage(fsys, e.Name)
		if err != nil {
			return nil, err
		}
		return []string{luaItem(p)}, nil
	case regindex.KindKBType:
		t, err := kbtype.Read(tree)
		if err != nil {
			return nil, err
		}
		if t.Name != e.Name {
			return nil, fmt.Errorf("kbtype.toml names %q, not %q", t.Name, e.Name)
		}
		items := make([]string, len(t.Folders))
		for i, f := range t.Folders {
			items[i] = "folder: " + f.Path
		}
		return items, nil
	}
	return nil, fmt.Errorf("kind %q", e.Kind)
}

// luaItem is what a Lua package asks for.
func luaItem(p manifests.Package) string {
	if len(p.Requires) == 0 {
		return "a Lua package, asking for nothing"
	}
	return "a Lua package, asking for: " + strings.Join(p.Requires, ", ")
}

// Verify checks what each entry's repo holds: every audited commit exists and is a commit, and the
// manifest parses there and at HEAD, naming the entry. A refused fetch is ErrUnverified, reported as
// such, never as a false audit. It returns each entry's declarations, read at its newest audited
// commit, or at HEAD when none is audited.
func Verify(ctx context.Context, src Source, entries []Entry, scratch string) (map[string]regindex.Declares, error) {
	out := map[string]regindex.Declares{}
	var errs []error
	for _, e := range entries {
		key := e.Kind + "/" + e.Name
		read := func(rev string) (regindex.Declares, error) {
			tree, commit, err := src.Checkout(ctx, e.Repo, rev, scratch)
			if err != nil {
				return regindex.Declares{}, err
			}
			defer os.RemoveAll(tree)
			items, err := Read(e, tree)
			if err != nil {
				return regindex.Declares{}, fmt.Errorf("at %s: %w", commit, err)
			}
			return regindex.Declares{Commit: commit, Items: items}, nil
		}
		head, err := read("HEAD")
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.File, err))
			continue
		}
		out[key] = head
		chosen := false // the newest audited commit that reads wins over HEAD
		for _, a := range newestFirst(e.Audits) {
			d, err := read(a.Commit)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: audit at %s: %w", e.File, a.Commit, err))
				continue
			}
			if !chosen {
				out[key], chosen = d, true
			}
		}
	}
	return out, errors.Join(errs...)
}

// newestFirst is audits by date, newest first; ties keep their order in the entry.
func newestFirst(audits []Audit) []Audit {
	out := append([]Audit(nil), audits...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Date.After(out[j-1].Date); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
