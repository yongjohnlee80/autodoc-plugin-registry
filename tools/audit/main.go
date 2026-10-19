// audit is Johno's weekly audit (ADR 1792351195 §4.4): for each entry whose HEAD has no audit yet,
// it checks the repo out at HEAD, has an agent read it headless with AutoDoc's own audit prompt, and
// writes the report and an [[audit]] on the entry. Then it opens a pull request with them. It never
// merges: the pull request is the proposal, and merging it is the mark.
//
//	go run ./tools/audit -model <model> [-by johno] [-root .] [-no-pr] [kind/name …]
//
// The agent is claude -p with only its reading tools. A run that fails, or says nothing, writes no
// [[audit]] and leaves the entry as it was.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc"
	"github.com/yongjohnlee80/autodoc-plugin-registry/registry"
)

func main() {
	root := flag.String("root", ".", "the registry's root")
	model := flag.String("model", "", "the model the agent runs, recorded on each audit (required)")
	by := flag.String("by", "johno", "who ran the audit, recorded on each audit")
	noPR := flag.Bool("no-pr", false, "write the reports and entries only; no branch or pull request")
	flag.Parse()
	if *model == "" {
		fmt.Fprintln(os.Stderr, "audit: -model is required")
		os.Exit(2)
	}
	if err := run(*root, *model, *by, !*noPR, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(root, model, by string, pr bool, only []string) error {
	if pr {
		if out, err := git(root, "status", "--porcelain"); err != nil || out != "" {
			return fmt.Errorf("audit: the registry checkout must be clean to open a pull request: %v%s", err, out)
		}
	}
	scratch, err := os.MkdirTemp("", "autodoc-registry-audit-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	a := auditor{root: root, model: model, by: by, src: registry.Git{}, agent: claude, now: time.Now, scratch: scratch, log: os.Stdout}
	done, err := a.all(context.Background(), only)
	if err != nil || len(done) == 0 || !pr {
		return err
	}
	return openPR(root, done)
}

// agentFunc runs the agent on task with tree as its one directory, and returns what it wrote, its
// exit status, and the tool it was (for the report's header).
type agentFunc func(ctx context.Context, model, tree, task string) (out []byte, status int, tool string, err error)

// claude is the agent: claude -p, allowed only to read, in the checkout.
func claude(ctx context.Context, model, tree, task string) ([]byte, int, string, error) {
	cmd := exec.CommandContext(ctx, "claude", "-p", task, "--model", model, "--add-dir", tree,
		"--allowedTools", "Read,Grep,Glob",
		"--disallowedTools", "Bash,Edit,Write,NotebookEdit,WebFetch,WebSearch",
		"--output-format", "text")
	cmd.Dir = tree
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out.Bytes(), exit.ExitCode(), "claude -p", nil
	}
	return out.Bytes(), 0, "claude -p", err
}

// auditor audits the registry at root.
type auditor struct {
	root, model, by string
	src             registry.Source
	agent           agentFunc
	now             func() time.Time
	scratch         string
	log             interface{ Write([]byte) (int, error) }
}

// all audits each entry (those only names, as kind/name, when it names any) whose HEAD has no
// audit, and says which it audited. A failed audit is logged and skipped; an unknown name is an
// error before anything runs. The registry is checked whole afterwards, as tools/check does.
func (a auditor) all(ctx context.Context, only []string) ([]string, error) {
	fsys := os.DirFS(a.root)
	entries, err := registry.Load(fsys)
	if err != nil {
		return nil, err
	}
	for _, o := range only {
		if !slices.ContainsFunc(entries, func(e registry.Entry) bool { return e.Kind+"/"+e.Name == o }) {
			return nil, fmt.Errorf("audit: no entry %s", o)
		}
	}
	var done []string
	for _, e := range entries {
		key := e.Kind + "/" + e.Name
		if len(only) > 0 && !slices.Contains(only, key) {
			continue
		}
		commit, err := a.one(ctx, e)
		switch {
		case err != nil:
			fmt.Fprintf(a.log, "%s: not audited: %v\n", key, err)
		case commit == "":
			fmt.Fprintf(a.log, "%s: HEAD is audited already\n", key)
		default:
			fmt.Fprintf(a.log, "%s: audited at %s\n", key, commit)
			done = append(done, key+"@"+commit[:7])
		}
	}
	if len(done) > 0 {
		entries, err := registry.Load(fsys)
		if err != nil {
			return done, err
		}
		if err := registry.Validate(fsys, entries); err != nil {
			return done, fmt.Errorf("audit: the registry no longer validates: %w", err)
		}
	}
	return done, nil
}

// one audits e at its repo's HEAD, and says the commit, or "" when that commit is audited already.
func (a auditor) one(ctx context.Context, e registry.Entry) (string, error) {
	tree, commit, err := a.src.Checkout(ctx, e.Repo, "HEAD", a.scratch)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tree)
	if slices.ContainsFunc(e.Audits, func(x registry.Audit) bool { return x.Commit == commit }) {
		return "", nil
	}
	actx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	out, status, tool, err := a.agent(actx, a.model, tree, task(e, tree, commit))
	switch {
	case err != nil:
		return "", fmt.Errorf("the agent did not run: %v", err)
	case status != 0:
		return "", fmt.Errorf("the agent exited %d", status)
	case len(bytes.TrimSpace(out)) == 0:
		return "", errors.New("the agent wrote nothing")
	}
	date := a.now().UTC().Format(time.DateOnly)
	report := registry.ReportPath(e.Kind, e.Name, commit)
	head := fmt.Sprintf("# Audit of %s %s at %s\n\n- Repo: %s\n- Commit: %s\n- Model: %s\n- Tool: %s\n- Exit status: %d\n- Date: %s\n- By: %s\n\n",
		e.Kind, e.Name, commit[:7], e.Repo, commit, a.model, tool, status, date, a.by)
	if err := os.MkdirAll(filepath.Join(a.root, filepath.Dir(report)), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(a.root, report), append([]byte(head), bytes.TrimSpace(out)...), 0o644); err != nil {
		return "", err
	}
	return commit, appendAudit(filepath.Join(a.root, e.File), fmt.Sprintf(
		"[[audit]]\ncommit = %q\nmodel = %q\ndate = %s\nby = %q\nreport = %q\n", commit, a.model, date, a.by, report))
}

// task is what the agent is asked: AutoDoc's audit prompt, then this audit's particulars.
func task(e registry.Entry, tree, commit string) string {
	return autodoc.AuditPrompt + fmt.Sprintf("\n## This audit\n\n- What: a %s, %q (%s)\n- The copy: %s\n- The commit: %s\n\n"+
		"Read the copy as the prompt says: do not run or change it. Write only the report.\n", e.Kind, e.Name, e.Repo, tree, commit)
}

// appendAudit adds block to the entry file as its own table, after a blank line, leaving what the
// file says above it as it was.
func appendAudit(file, block string) error {
	src, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	src = append(bytes.TrimRight(src, "\n"), "\n\n"...)
	return os.WriteFile(file, append(src, block...), 0o644)
}

// openPR puts the audits on a branch of their own and opens a pull request for them.
func openPR(root string, done []string) error {
	branch := "audit/" + time.Now().UTC().Format("2006-01-02-150405")
	title := "audits: " + strings.Join(done, ", ")
	for _, args := range [][]string{
		{"switch", "-c", branch},
		{"add", "entries", "audits"},
		{"commit", "-m", title},
		{"push", "-u", "origin", branch},
	} {
		if _, err := git(root, args...); err != nil {
			return err
		}
	}
	cmd := exec.Command("gh", "pr", "create", "--base", "main", "--head", branch, "--title", title,
		"--body", "The weekly audit (tools/audit). Read each report before merging: merging marks its commit audited.")
	cmd.Dir, cmd.Stdout, cmd.Stderr = root, os.Stdout, os.Stderr
	return cmd.Run()
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(out.String()), nil
}
