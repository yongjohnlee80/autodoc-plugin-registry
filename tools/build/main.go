// build writes the registry's site: index.json, which AutoDoc fetches, a page listing the entries,
// one page per entry and the audit reports (ADR 1792351195 §4.1). Each entry's declarations are read
// from its manifest at its newest audited commit, or at HEAD when none is audited. The index's time
// is the registry's own last commit, so the same entries at the same commits give the same bytes.
//
//	go run ./tools/build [-root .] -out site
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc-plugin-registry/registry"
)

func main() {
	root := flag.String("root", ".", "the registry's root")
	out := flag.String("out", "site", "the directory the site is written to")
	flag.Parse()
	if err := run(*root, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(root, out string) error {
	fsys := os.DirFS(root)
	entries, err := registry.Load(fsys)
	if err != nil {
		return err
	}
	if err := registry.Validate(fsys, entries); err != nil {
		return err
	}
	scratch, err := os.MkdirTemp("", "autodoc-registry-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	declares, err := registry.Verify(ctx, registry.Git{}, entries, scratch)
	if err != nil {
		return err
	}
	generated, err := lastCommitTime(root)
	if err != nil {
		return err
	}
	if err := registry.WriteSite(out, fsys, registry.BuildIndex(entries, declares, generated)); err != nil {
		return err
	}
	fmt.Printf("%d entries written to %s\n", len(entries), out)
	return nil
}

// lastCommitTime is when the registry's HEAD was committed.
func lastCommitTime(root string) (time.Time, error) {
	cmd := exec.Command("git", "-C", root, "log", "-1", "--format=%cI")
	b, err := cmd.Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("the registry's last commit: %w", err)
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(string(b)))
}
