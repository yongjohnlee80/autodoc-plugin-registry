package registry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Source fetches an entry's repo at a commit into a directory, for its manifest to be read there. The
// registry's tools use Git; tests use a fake.
type Source interface {
	// Checkout puts the repo's tree at rev (a full commit, or "HEAD" for its default branch's head)
	// into a new directory under dir, and says which commit that is. Nothing in the repo is run.
	Checkout(ctx context.Context, repo, rev, dir string) (tree, commit string, err error)
}

// ErrUnverified is a fetch the host refused or that failed: it is never read as proof that a commit
// is absent, or that an audit is false.
var ErrUnverified = errors.New("could not verify")

// Git is a Source over the git command line: a shallow fetch of the one commit asked for, into a
// fresh repository, with no hooks, templates or credential helpers.
type Git struct{}

func (Git) Checkout(ctx context.Context, repo, rev, dir string) (string, string, error) {
	tree, err := os.MkdirTemp(dir, "repo-")
	if err != nil {
		return "", "", err
	}
	run := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{
			"-c", "core.hooksPath=/dev/null", "-c", "credential.helper=", "-c", "protocol.allow=never",
			"-c", "protocol.https.allow=always",
		}, args...)...)
		cmd.Dir = tree
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_TEMPLATE_DIR=")
		var out, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(stderr.String()))
		}
		return strings.TrimSpace(out.String()), nil
	}
	if _, err := run("init", "-q"); err != nil {
		return "", "", err
	}
	if _, err := run("fetch", "-q", "--depth", "1", "--no-tags", repo, rev); err != nil {
		return "", "", fmt.Errorf("%w: %s at %s: %v", ErrUnverified, repo, rev, err)
	}
	kind, err := run("cat-file", "-t", "FETCH_HEAD")
	if err != nil {
		return "", "", err
	}
	if kind != "commit" {
		return "", "", fmt.Errorf("%s at %s is a %s, not a commit", repo, rev, kind)
	}
	commit, err := run("rev-parse", "FETCH_HEAD")
	if err != nil {
		return "", "", err
	}
	if rev != "HEAD" && commit != rev {
		return "", "", fmt.Errorf("%s at %s fetched %s", repo, rev, commit)
	}
	if _, err := run("checkout", "-q", "--detach", "FETCH_HEAD"); err != nil {
		return "", "", err
	}
	if err := os.RemoveAll(filepath.Join(tree, ".git")); err != nil {
		return "", "", err
	}
	return tree, commit, nil
}
