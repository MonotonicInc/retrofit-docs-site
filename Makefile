# The site renders the docs tree retrofit generates; none of it is committed.
# RETROFIT is a retrofit checkout. `tree` writes its docs tree into _tree/,
# which hugo.toml mounts as the content directory and the manifest's data.

RETROFIT ?= ../retrofit
HUGO ?= hugo
HUGO_FLAGS ?=
TREE := $(CURDIR)/_tree

.PHONY: build serve tree check style-check style-sync clean

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

clean:
	rm -rf "$(TREE)" public resources/_gen
