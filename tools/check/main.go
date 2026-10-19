// check validates the registry's entries, as CI runs it on every pull request (ADR 1792351195 §4.3):
// each entry's schema and file, its repo URL and its audits' reports. Then, unless -offline, what each
// repo holds: every audited commit exists and is a commit, and the manifest parses there and at
// HEAD, naming the entry, read by AutoDoc's own readers. Nothing from a repo is built or run.
//
//	go run ./tools/check [-root .] [-offline]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/yongjohnlee80/autodoc-plugin-registry/registry"
)

func main() {
	root := flag.String("root", ".", "the registry's root")
	offline := flag.Bool("offline", false, "check the entries only; fetch no repo")
	flag.Parse()
	if err := run(*root, *offline); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(root string, offline bool) error {
	fsys := os.DirFS(root)
	entries, err := registry.Load(fsys)
	if err != nil {
		return err
	}
	if err := registry.Validate(fsys, entries); err != nil {
		return err
	}
	fmt.Printf("%d entries: schema, files, repos and reports ok\n", len(entries))
	if offline {
		return nil
	}
	scratch, err := os.MkdirTemp("", "autodoc-registry-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	declares, err := registry.Verify(ctx, registry.Git{}, entries, scratch)
	for _, e := range entries {
		if d, ok := declares[e.Kind+"/"+e.Name]; ok {
			fmt.Printf("%s %s: manifest ok at %s\n", e.Kind, e.Name, d.Commit)
		}
	}
	return err
}
