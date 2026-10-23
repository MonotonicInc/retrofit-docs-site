# docs.retrofit.sh: read this first

## This repository holds no docs content

The site renders a tree retrofit generates (`go run ./cmd/docs -out <dir>`
in a retrofit checkout). This repository holds the theme, layouts and
configuration only.

- **Prose edits go to retrofit**, in `docs/*.md`. So do navigation
  (`docs/nav.yaml`), page titles and descriptions, the command reference
  (retrofit's own `--help`), the reference tables and the release notes.
- **Never commit content.** `_tree/` is generated and ignored. Do not add
  `content/`, a copied page, or front matter that restates the manifest.
- If a page renders wrong because of what retrofit generates, fix the
  generator or the source doc in retrofit, not here.

## Build

```sh
make build        # RETROFIT=../retrofit by default
make serve
make check        # build, then scripts/check-nav
make versions-test
```

The published site is built from releases: `make versions` builds what
`versions.toml` publishes from each release's docs bundle. Before 1.0 that
is `/current/` only, the newest full release of any line (the newest WIP
until the first full release); from 1.0, `publish = "lines"` adds `/X.Y/`
per line (README, Versions). Do not list lines or add a `/X.Y/` path before
1.0. A bundle is checked against its release's `checksums.txt`; never
weaken or skip that check. `versions/` is
its own Go module so `go mod tidy` cannot touch the Hugo module's `go.mod`
at the root.

Two checks fail the build: a page `manifest.json` names with no page in the
build (`layouts/_partials/manifest-check.html`), and a link between docs to a
page the tree does not have (hugo-book's portable links, set to `error`).
`scripts/check-nav` checks the rendered navigation against the manifest.

## Navigation

The left navigation, its order and its titles are `manifest.json`'s, which
retrofit writes from `docs/nav.yaml`. Do not add entries, reorder, or retitle
here; change `docs/nav.yaml` in retrofit.

## Design and voice

retrofit-site's `avw_retrofit-design-system.md` governs what a reader sees.
`assets/css/tokens.css`, `assets/css/fonts.css`, `static/fonts/` and
`static/favicon.svg` are verbatim copies pinned in `style.lock`: never edit
them here. Change them in retrofit-site, commit there, then `make style-sync`.
`make style-check` fails on any difference, and CI runs it. The restyle is
`assets/styles/custom.css`; it reads the tokens' spacing scale as
`--rf-space-N` (the build renames it, since hugo-book uses `--space-N` too)
and spends nothing the Don't list rules out: no radius, shadows or
gradients, and no spot on anything you can click.
The docs register, the design system's Voice rules for reference docs, lives
in retrofit. Rendered copy this repository adds (layouts, `hugo.toml` params
that reach the page) follows both: no em dashes or middots, and the name is
lowercase `retrofit`.

## Pins

- Hugo: `HUGO_VERSION` in `netlify.toml`, matching retrofit-site's.
- hugo-book: `go.mod`. Move it with
  `hugo mod get github.com/alex-shpak/hugo-book@<commit>`, then build,
  check the warnings, and diff `layouts/baseof.html`,
  `layouts/_partials/docs/html-head.html`,
  `layouts/_partials/docs/prev-next.html` and
  `layouts/_partials/docs/html-head-favicon.html` against the theme's copies.
- Design system: `style.lock` (a retrofit-site commit).
