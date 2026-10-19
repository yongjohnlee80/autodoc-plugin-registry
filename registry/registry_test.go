package registry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

const sha1 = "1111111111111111111111111111111111111111"
const sha2 = "2222222222222222222222222222222222222222"

func entryFS(files map[string]string) fstest.MapFS {
	fs := fstest.MapFS{}
	for p, s := range files {
		fs[p] = &fstest.MapFile{Data: []byte(s)}
	}
	return fs
}

const plugin = "kind = \"plugin\"\nname = \"autodb\"\ntitle = \"AutoDB\"\ndescription = \"d\"\nrepo = \"https://github.com/o/autodb\"\nauthor = \"o\"\n"

// Load refuses a key the schema doesn't have and an entry filed under another kind's directory.
func TestLoadRefusesUnknownKeysAndMisfiledEntries(t *testing.T) {
	_, err := Load(entryFS(map[string]string{"entries/plugins/autodb.toml": plugin + "capabilities = [\"x\"]\n"}))
	if err == nil || !strings.Contains(err.Error(), `unknown key "capabilities"`) {
		t.Fatalf("unknown key: %v", err)
	}
	_, err = Load(entryFS(map[string]string{"entries/lua/autodb.toml": plugin}))
	if err == nil || !strings.Contains(err.Error(), `kind "plugin", but it is under entries/lua/`) {
		t.Fatalf("misfiled: %v", err)
	}
	es, err := Load(entryFS(map[string]string{"entries/plugins/autodb.toml": plugin, "entries/kb-types/para.kb.toml": strings.NewReplacer(`"plugin"`, `"kb-type"`, `"autodb"`, `"para.kb"`).Replace(plugin)}))
	if err != nil || len(es) != 2 || es[0].Kind != KindKBType || es[1].Name != "autodb" {
		t.Fatalf("%v, %+v; want both, kb-type first", err, es)
	}
}

// Validate refuses each rule with its own reason.
func TestValidateRefusesEachRule(t *testing.T) {
	audit := func(commit, report string) string {
		return fmt.Sprintf("[[audit]]\ncommit = %q\nmodel = \"m\"\ndate = 2026-10-19\nby = \"johno\"\nreport = %q\n", commit, report)
	}
	good := ReportPath(KindPlugin, "autodb", sha1)
	for _, c := range []struct{ name, entry, file, want string }{
		{"the file named for another", plugin, "entries/plugins/other.toml", `the file is named for "other.toml", but the entry is "autodb"`},
		{"no title", strings.Replace(plugin, `title = "AutoDB"`, `title = " "`, 1), "", "no title"},
		{"http", strings.Replace(plugin, "https://", "http://", 1), "", "want an https:// URL"},
		{"another host", strings.Replace(plugin, "github.com", "example.com", 1), "", `host "example.com"`},
		{"a deep path", strings.Replace(plugin, "o/autodb", "o/autodb/tree/main", 1), "", "want https://<host>/<owner>/<repo>"},
		{"a short commit", plugin + audit("1111111", good), "", "want the full 40-character SHA"},
		{"a report elsewhere", plugin + audit(sha1, "audits/autodb.md"), "", "want \"" + good + "\""},
		{"a missing report", plugin + audit(sha2, ReportPath(KindPlugin, "autodb", sha2)), "", "is missing"},
		{"twice", plugin + audit(sha1, good) + audit(sha1, good), "", "audited twice"},
	} {
		file := c.file
		if file == "" {
			file = "entries/plugins/autodb.toml"
		}
		fsys := entryFS(map[string]string{file: c.entry, good: "report"})
		es, err := Load(fsys)
		if err == nil {
			err = Validate(fsys, es)
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v; want %q", c.name, err, c.want)
		}
	}
	fsys := entryFS(map[string]string{"entries/plugins/autodb.toml": plugin + audit(sha1, good), good: "report"})
	es, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(fsys, es); err != nil {
		t.Fatalf("a good entry: %v", err)
	}
}

// fakeSource serves prepared trees: commit → files; "HEAD" is head.
type fakeSource struct {
	head  string
	trees map[string]map[string]string
	fail  map[string]bool
}

func (f fakeSource) Checkout(_ context.Context, _, rev, dir string) (string, string, error) {
	commit := rev
	if rev == "HEAD" {
		commit = f.head
	}
	if f.fail[commit] {
		return "", "", fmt.Errorf("%w: refused", ErrUnverified)
	}
	files, ok := f.trees[commit]
	if !ok {
		return "", "", fmt.Errorf("%s is not a commit", rev)
	}
	tree, err := os.MkdirTemp(dir, "tree-")
	if err != nil {
		return "", "", err
	}
	for p, s := range files {
		full := filepath.Join(tree, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return "", "", err
		}
		if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
			return "", "", err
		}
	}
	return tree, commit, nil
}

const pluginToml = "name = \"autodb\"\nkind = \"service\"\nprotocol = 4\ncommand = [\"./autodb\"]\nstart = \"launch\"\n"

// Verify reads each manifest at HEAD and at every audited commit; the newest audited commit's
// declarations are the index's; a refused fetch is ErrUnverified; a manifest naming another
// plugin is refused.
func TestVerify(t *testing.T) {
	e := Entry{Kind: KindPlugin, Name: "autodb", Repo: "https://github.com/o/autodb", File: "entries/plugins/autodb.toml",
		Audits: []Audit{{Commit: sha1, Date: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}, {Commit: sha2, Date: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)}}}
	lua := "name = \"autodb\"\nrequires = [\"clipboard\"]\n"
	src := fakeSource{head: "3333333333333333333333333333333333333333", trees: map[string]map[string]string{
		"3333333333333333333333333333333333333333": {"plugin.toml": pluginToml},
		sha1: {"plugin.toml": pluginToml},
		sha2: {"plugin.toml": pluginToml, "lua/package.toml": lua, "lua/init.lua": "return {}"},
	}}
	d, err := Verify(context.Background(), src, []Entry{e}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got := d["plugin/autodb"]
	if got.Commit != sha2 || !strings.Contains(strings.Join(got.Items, "|"), "asking for: clipboard") ||
		!strings.Contains(strings.Join(got.Items, "|"), "starts with AutoDoc") {
		t.Fatalf("declares %+v; want the newest audit's, with its Lua package", got)
	}

	src.fail = map[string]bool{sha1: true}
	if _, err := Verify(context.Background(), src, []Entry{e}, t.TempDir()); !errors.Is(err, ErrUnverified) {
		t.Fatalf("a refused fetch: %v; want ErrUnverified", err)
	}

	src.fail = nil
	src.trees[src.head] = map[string]string{"plugin.toml": strings.Replace(pluginToml, `"autodb"`, `"other"`, 1)}
	if _, err := Verify(context.Background(), src, []Entry{e}, t.TempDir()); err == nil || !strings.Contains(err.Error(), `plugin.toml names "other", not "autodb"`) {
		t.Fatalf("another plugin's manifest: %v", err)
	}
}

// The same entries at the same commits give the same bytes, and the site carries the reports.
func TestTheBuildIsDeterministic(t *testing.T) {
	es := []Entry{{Kind: KindPlugin, Name: "autodb", Title: "AutoDB", Description: "d", Repo: "https://github.com/o/autodb", Author: "o",
		Audits: []Audit{{Commit: sha1, Model: "m", By: "johno", Date: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC), Report: ReportPath(KindPlugin, "autodb", sha1)}}}}
	d := map[string]Declares{"plugin/autodb": {Commit: sha1, Items: []string{"a card beside the page"}}}
	when := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	a, err := BuildIndex(es, d, when).JSON()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := BuildIndex(es, d, when).JSON()
	if !bytes.Equal(a, b) {
		t.Fatal("two builds differ")
	}
	if !bytes.Contains(a, []byte(`"report": "`+PagesURL+"audits/plugin/autodb/"+sha1+`.md"`)) || !bytes.Contains(a, []byte(`"format": 1`)) {
		t.Fatalf("index:\n%s", a)
	}
	out := t.TempDir()
	root := entryFS(map[string]string{ReportPath(KindPlugin, "autodb", sha1): "the report"})
	if err := WriteSite(out, root, BuildIndex(es, d, when)); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"index.json", "index.html", "plugin/autodb/index.html", "audits/plugin/autodb/" + sha1 + ".md", ".nojekyll"} {
		if _, err := os.Stat(filepath.Join(out, p)); err != nil {
			t.Errorf("site lacks %s", p)
		}
	}
}
