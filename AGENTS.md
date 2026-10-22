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
```

Two checks fail the build: a page `manifest.json` names with no page in the
build (`layouts/_partials/manifest-check.html`), and a link between docs to a
page the tree does not have (hugo-book's portable links, set to `error`).

## Design and voice

retrofit-site's `avw_retrofit-design-system.md` governs what a reader sees.
The docs register, the design system's Voice rules for reference docs, lives
in retrofit. Rendered copy this repository adds (layouts, `hugo.toml` params
that reach the page) follows both: no em dashes or middots, and the name is
lowercase `retrofit`.

## Pins

- Hugo: `HUGO_VERSION` in `netlify.toml`, matching retrofit-site's.
- hugo-book: `go.mod`. Move it with
  `hugo mod get github.com/alex-shpak/hugo-book@<commit>`, then build and
  check the warnings.
