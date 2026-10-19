# AutoDoc registry

The plugins, Lua packages and KB types [AutoDoc](https://github.com/yongjohnlee80/autodoc) can
install by name, each with the audits made of it. AutoDoc fetches the index this repo publishes:

    https://yongjohnlee80.github.io/autodoc-plugin-registry/index.json

and lists it in **Plugins › Browse the registry…** and in the Add-workspace dialog's KB type field.
A manual git URL still works everywhere; the registry only names repos, it never hosts their code.

## What an audit is

An audit is an AI model's reading of an entry's code at **one full commit**, recorded in
`audits/<kind>/<name>/<commit>.md` with an `[[audit]]` on the entry. AutoDoc shows "✓ audited by
<model> on <date> at this commit" only when the commit it is about to install is that commit; any
other commit says plainly that it isn't audited. An audit is advice, not a sandbox, and AutoDoc's
risk warning shows on every install all the same.

## Layout

```text
entries/
  plugins/<name>.toml      a plugin: its repo has plugin.toml at the top
  lua/<name>.toml          a Lua package: package.toml and init.lua at the top
  kb-types/<name>.toml     a KB type: kbtype.toml and tree/ at the top
audits/<kind>/<name>/<full commit>.md   an audit's report
registry/                  the entries' schema, checks, and the index build (Go)
tools/check                validates every entry: CI runs it on each pull request
tools/build                writes the site: index.json, the pages and the reports
```

An entry:

```toml
kind = "plugin"                  # plugin | lua | kb-type
name = "autodb"                  # the manifest's own name, and the file's
title = "AutoDB"
description = "One line on what it is."
repo = "https://github.com/yongjohnlee80/autodb"   # https on github.com, gitlab.com or codeberg.org
author = "yongjohnlee80"

[[audit]]
commit = "<full 40-character SHA>"   # a tag can move; a commit can't
model = "<the model that read it>"
date = 2026-10-19
by = "<who ran it>"
report = "audits/plugin/autodb/<full SHA>.md"
```

An entry never lists what its code does: `tools/build` reads it from the repo's own manifest, at
the newest audited commit (or HEAD when none is), with AutoDoc's own readers (`core/manifests`,
`core/kbtype`), so the registry and AutoDoc never disagree on what a manifest means.

## Adding an entry

1. Add `entries/<kind dir>/<name>.toml`, named for the manifest's `name`.
2. Run `go run ./tools/check`. It checks the schema, the repo URL, each audit's report, that every
   audited commit exists and is a commit, and that the manifest parses at HEAD and at each audited
   commit, naming the entry. Nothing from the repo is built or run.
3. Open a pull request. CI runs the same check; merging publishes the site.

## License

Apache-2.0, as AutoDoc.
