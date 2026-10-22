# The site renders the docs tree retrofit generates; none of it is committed.
# RETROFIT is a retrofit checkout. `tree` writes its docs tree into _tree/,
# which hugo.toml mounts as the content directory and the manifest's data.

RETROFIT ?= ../retrofit
HUGO ?= hugo
HUGO_FLAGS ?=
TREE := $(CURDIR)/_tree

# The versioned site (versions/): one build per line in versions.toml at
# /X.Y/, the newest again at /current/. Bundles come from each line's newest
# full release on GitHub, or from RELEASES_DIR/<tag>/ when it is set.
BASE_URL ?= https://docs.retrofit.sh/
RELEASES_DIR ?=
VERSIONS := $(CURDIR)/_bin/versions

.PHONY: build serve tree check style-check style-sync versions versions-test fixture clean

build: tree
	$(HUGO) --gc --minify $(HUGO_FLAGS)

serve: tree
	$(HUGO) server $(HUGO_FLAGS)

# Checks on the rendered site: the navigation against the manifest.
check: build
	scripts/check-nav public

# The design system copies against style.lock, and against retrofit-site's
# HEAD when a checkout is at RETROFIT_SITE (default ../retrofit-site).
style-check:
	scripts/style-check

# Re-copy the design system files from a retrofit-site commit (REF, default
# HEAD) and rewrite style.lock.
style-sync:
	scripts/style-sync $(REF)

tree:
	@test -d "$(RETROFIT)/cmd/docs" || { echo "RETROFIT=$(RETROFIT) is not a retrofit checkout (no cmd/docs)" >&2; exit 1; }
	rm -rf "$(TREE)"
	cd "$(RETROFIT)" && go run ./cmd/docs -out "$(TREE)"

$(VERSIONS): $(wildcard versions/*.go) versions/go.mod versions/go.sum
	go -C versions build -o "$(VERSIONS)" .

versions: $(VERSIONS)
	"$(VERSIONS)" build -root . -out public -base-url "$(BASE_URL)" -hugo "$(HUGO)" $(if $(RELEASES_DIR),-releases-dir "$(RELEASES_DIR)")

versions-test:
	go -C versions test ./...

# A release-shaped bundle of this retrofit checkout's docs tree, under
# _fixtures/bin-v<version>/, until releases carry one:
#   make fixture && make versions RELEASES_DIR=_fixtures
fixture: tree $(VERSIONS)
	"$(VERSIONS)" pack -tree "$(TREE)" -out _fixtures

clean:
	rm -rf "$(TREE)" public resources/_gen _bin _versions _fixtures
