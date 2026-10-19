package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc"
	"github.com/yongjohnlee80/autodoc-plugin-registry/registry"
)

const head = "3333333333333333333333333333333333333333"
const old = "1111111111111111111111111111111111111111"

const entry = "kind = \"plugin\"\nname = \"autodb\"\ntitle = \"AutoDB\"\ndescription = \"d\"\nrepo = \"https://github.com/o/autodb\"\nauthor = \"o\"\n"

// headSource checks out a one-file tree at head, or fails as a refused fetch.
type headSource struct{ refuse bool }

func (s headSource) Checkout(_ context.Context, _, rev, dir string) (string, string, error) {
	if s.refuse {
		return "", "", fmt.Errorf("%w: refused", registry.ErrUnverified)
	}
	if rev != "HEAD" {
		return "", "", fmt.Errorf("asked for %s; want HEAD", rev)
	}
	tree, err := os.MkdirTemp(dir, "tree-")
	if err != nil {
		return "", "", err
	}
	return tree, head, os.WriteFile(filepath.Join(tree, "plugin.toml"), []byte("name = \"autodb\"\n"), 0o644)
}

// registryAt writes a registry root holding the autodb entry, plus extra (appended to it).
func registryAt(t *testing.T, extra string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files["entries/plugins/autodb.toml"] = entry + extra
	for p, s := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func newAuditor(root string, agent agentFunc, src registry.Source, t *testing.T) (auditor, *bytes.Buffer) {
	var log bytes.Buffer
	return auditor{root: root, model: "m", by: "johno", src: src, agent: agent, scratch: t.TempDir(), log: &log,
		now: func() time.Time { return time.Date(2026, 10, 19, 23, 0, 0, 0, time.FixedZone("KST", 9*3600)) }}, &log
}

// An unaudited HEAD gets its report, headed, and an [[audit]] after what the entry said; the agent
// was handed AutoDoc's own prompt with the copy and the full commit; the registry still validates.
func TestAnUnauditedHeadIsAuditedAndRecorded(t *testing.T) {
	root := registryAt(t, "", map[string]string{})
	var asked, tree string
	agent := func(_ context.Context, model, dir, task string) ([]byte, int, string, error) {
		asked, tree = task, dir
		if _, err := os.Stat(filepath.Join(dir, "plugin.toml")); err != nil {
			t.Errorf("the agent's directory isn't the checkout: %v", err)
		}
		return []byte("\nVerdict: no concerns found.\n\n"), 0, "fake", nil
	}
	a, _ := newAuditor(root, agent, headSource{}, t)
	done, err := a.all(context.Background(), nil)
	if err != nil || len(done) != 1 || done[0] != "plugin/autodb@3333333" {
		t.Fatalf("done %v, %v", done, err)
	}
	if !strings.HasPrefix(asked, autodoc.AuditPrompt) || !strings.Contains(asked, "The copy: "+tree) || !strings.Contains(asked, "The commit: "+head) {
		t.Fatalf("the task:\n%s", asked)
	}
	report, err := os.ReadFile(filepath.Join(root, registry.ReportPath("plugin", "autodb", head)))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"- Commit: " + head, "- Model: m", "- Tool: fake", "- Exit status: 0", "- Date: 2026-10-19", "Verdict: no concerns found."} {
		if !bytes.Contains(report, []byte(want)) {
			t.Errorf("the report lacks %q:\n%s", want, report)
		}
	}
	got, _ := os.ReadFile(filepath.Join(root, "entries/plugins/autodb.toml"))
	if !strings.HasPrefix(string(got), entry+"\n[[audit]]\ncommit = \""+head+"\"") || !strings.Contains(string(got), "date = 2026-10-19\n") {
		t.Fatalf("the entry:\n%s", got)
	}
	es, err := registry.Load(os.DirFS(root))
	if err != nil || len(es[0].Audits) != 1 || es[0].Audits[0].By != "johno" {
		t.Fatalf("%+v, %v", es, err)
	}
}

// A HEAD already audited runs no agent and writes nothing.
func TestAnAuditedHeadIsLeftAlone(t *testing.T) {
	audit := fmt.Sprintf("\n[[audit]]\ncommit = %q\nmodel = \"m\"\ndate = 2026-10-01\nby = \"johno\"\nreport = %q\n", head, registry.ReportPath("plugin", "autodb", head))
	root := registryAt(t, audit, map[string]string{registry.ReportPath("plugin", "autodb", head): "r"})
	a, log := newAuditor(root, func(context.Context, string, string, string) ([]byte, int, string, error) {
		t.Error("the agent ran")
		return nil, 0, "", nil
	}, headSource{}, t)
	if done, err := a.all(context.Background(), nil); err != nil || len(done) != 0 {
		t.Fatalf("done %v, %v", done, err)
	}
	if !strings.Contains(log.String(), "HEAD is audited already") {
		t.Fatalf("log: %s", log)
	}
}

// A failed, silent or unstarted agent, or a refused fetch, writes no report and leaves the entry
// as it was; an older audit doesn't count for HEAD.
func TestAFailedAuditWritesNothing(t *testing.T) {
	older := fmt.Sprintf("\n[[audit]]\ncommit = %q\nmodel = \"m\"\ndate = 2026-10-01\nby = \"johno\"\nreport = %q\n", old, registry.ReportPath("plugin", "autodb", old))
	for _, c := range []struct {
		name  string
		agent agentFunc
		src   registry.Source
		want  string
	}{
		{"exit 1", func(context.Context, string, string, string) ([]byte, int, string, error) {
			return []byte("half"), 1, "", nil
		}, headSource{}, "exited 1"},
		{"silent", func(context.Context, string, string, string) ([]byte, int, string, error) {
			return []byte(" \n"), 0, "", nil
		}, headSource{}, "wrote nothing"},
		{"not run", func(context.Context, string, string, string) ([]byte, int, string, error) {
			return nil, 0, "", errors.New("no claude")
		}, headSource{}, "did not run"},
		{"refused", nil, headSource{refuse: true}, "could not verify"},
	} {
		root := registryAt(t, older, map[string]string{registry.ReportPath("plugin", "autodb", old): "r"})
		a, log := newAuditor(root, c.agent, c.src, t)
		done, err := a.all(context.Background(), nil)
		if err != nil || len(done) != 0 || !strings.Contains(log.String(), c.want) {
			t.Errorf("%s: done %v, %v, log %q", c.name, done, err, log)
		}
		if _, err := os.Stat(filepath.Join(root, registry.ReportPath("plugin", "autodb", head))); err == nil {
			t.Errorf("%s: a report was written", c.name)
		}
		if got, _ := os.ReadFile(filepath.Join(root, "entries/plugins/autodb.toml")); string(got) != entry+older {
			t.Errorf("%s: the entry changed:\n%s", c.name, got)
		}
	}
}

// Naming an entry that doesn't exist is refused before anything runs; naming one audits it alone.
func TestOnlyNamesEntries(t *testing.T) {
	root := registryAt(t, "", map[string]string{})
	ran := 0
	a, _ := newAuditor(root, func(context.Context, string, string, string) ([]byte, int, string, error) {
		ran++
		return []byte("ok"), 0, "fake", nil
	}, headSource{}, t)
	if _, err := a.all(context.Background(), []string{"plugin/nope"}); err == nil || !strings.Contains(err.Error(), "no entry plugin/nope") || ran != 0 {
		t.Fatalf("%v, ran %d", err, ran)
	}
	if done, err := a.all(context.Background(), []string{"plugin/autodb"}); err != nil || len(done) != 1 || ran != 1 {
		t.Fatalf("done %v, %v, ran %d", done, err, ran)
	}
}
