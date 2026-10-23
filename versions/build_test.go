package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// siteRoot copies the site's own files (not _tree, public or the Go module
// here) into a temporary root, so a build there cannot touch the checkout.
func siteRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{"hugo.toml", "go.mod", "go.sum", "layouts", "assets", "static"} {
		src := filepath.Join("..", p)
		fi, err := os.Stat(src)
		if err != nil {
			t.Fatal(err)
		}
		if fi.IsDir() {
			err = copyDir(src, filepath.Join(root, p))
		} else {
			var body []byte
			if body, err = os.ReadFile(src); err == nil {
				err = os.WriteFile(filepath.Join(root, p), body, 0o644)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// releases packs fixture trees into a release directory: two releases of
// 0.10 (the newer must win) and one of 0.11.
func releases(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, r := range []struct {
		tag, version, line string
		pages              []string
	}{
		{"bin-v0.10.0", "0.10.0", "0.10", []string{"a.md"}},
		{"bin-v0.10.1", "0.10.1", "0.10", []string{"a.md", "gone.md", "commands/b.md"}},
		{"v0.11.0", "0.11.0", "0.11", []string{"a.md", "commands/b.md", "new.md"}},
	} {
		tree := t.TempDir()
		writeTree(t, tree, r.version, r.line, r.pages)
		if err := pack(tree, r.tag, dir, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func hugoBin(t *testing.T) string {
	t.Helper()
	hugo, err := exec.LookPath("hugo")
	if err != nil {
		t.Fatal("the versioned build runs Hugo; put hugo on PATH")
	}
	return hugo
}

func read(t *testing.T, p string) string {
	t.Helper()
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

var canonicalRE = regexp.MustCompile(`<link rel="?canonical"? href="?([^" >]+)`)

func canonical(t *testing.T, page string) string {
	t.Helper()
	m := canonicalRE.FindStringSubmatch(read(t, page))
	if m == nil {
		t.Fatalf("%s has no canonical link", page)
	}
	return m[1]
}

var switcherRE = regexp.MustCompile(`<li><a href="?([^" >]+)"?[^>]*>([0-9.]+)</a>`)

func switcher(t *testing.T, page string) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, m := range switcherRE.FindAllStringSubmatch(read(t, page), -1) {
		got[m[2]] = m[1]
	}
	return got
}

func TestBuildTwoLines(t *testing.T) {
	root, rels, hugo := siteRoot(t), releases(t), hugoBin(t)
	out := filepath.Join(t.TempDir(), "public")
	var log bytes.Buffer
	err := build(buildInput{
		root: root, out: out, baseURL: "https://docs.example/", publish: publishLines,
		lines: []string{"0.10", "0.11"}, src: dirSource{dir: rels}, hugo: hugo, log: &log,
	})
	if err != nil {
		t.Fatalf("build: %v\n%s", err, log.String())
	}

	// Both lines at /X.Y/, the newest again at /current/.
	for _, p := range []string{
		"0.10/a/index.html", "0.10/gone/index.html", "0.10/commands/b/index.html",
		"0.11/a/index.html", "0.11/new/index.html",
		"current/a/index.html", "current/new/index.html",
	} {
		if _, err := os.Stat(filepath.Join(out, p)); err != nil {
			t.Errorf("missing %s", p)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "current", "gone")); err == nil {
		t.Error("/current/ has 0.10's gone page; want 0.11's tree")
	}
	if !strings.Contains(read(t, filepath.Join(out, "0.10", "a", "index.html")), "of 0.10.1") {
		t.Error("/0.10/ is not built from the newest 0.10 release, bin-v0.10.1")
	}
	if !strings.Contains(read(t, filepath.Join(out, "current", "a", "index.html")), "of 0.11.0") {
		t.Error("/current/ is not built from 0.11.0")
	}

	// The root: a redirect to /current/, the fonts, a 404 page.
	if got := read(t, filepath.Join(out, "_redirects")); got != "/  /current/  301\n" {
		t.Errorf("_redirects = %q", got)
	}
	if _, err := os.Stat(filepath.Join(out, "fonts", "IBMPlexSerif-Regular.woff2")); err != nil {
		t.Error("no fonts at /fonts/, where fonts.css loads them")
	}
	if _, err := os.Stat(filepath.Join(out, "404.html")); err != nil {
		t.Error("no root 404.html")
	}

	// /current/ is canonical wherever the newest line has the page.
	for page, want := range map[string]string{
		"0.10/a/index.html":          "https://docs.example/current/a/",
		"0.10/commands/b/index.html": "https://docs.example/current/commands/b/",
		"0.10/gone/index.html":       "https://docs.example/0.10/gone/",
		"0.10/index.html":            "https://docs.example/current/",
		"0.11/new/index.html":        "https://docs.example/current/new/",
		"current/a/index.html":       "https://docs.example/current/a/",
	} {
		if got := canonical(t, filepath.Join(out, page)); got != want {
			t.Errorf("%s canonical = %s; want %s", page, got, want)
		}
	}

	// The switcher: the same page in each line, or the line's first page;
	// the newest line through /current/.
	for page, want := range map[string]map[string]string{
		"0.10/a/index.html":      {"0.11.0": "/current/a/", "0.10.1": "/0.10/a/"},
		"0.10/gone/index.html":   {"0.11.0": "/current/a/", "0.10.1": "/0.10/gone/"},
		"current/new/index.html": {"0.11.0": "/current/new/", "0.10.1": "/0.10/a/"},
	} {
		got := switcher(t, filepath.Join(out, page))
		for v, href := range want {
			if got[v] != href {
				t.Errorf("%s switcher %s -> %q; want %q (all: %v)", page, v, got[v], href, got)
			}
		}
	}
}

// Before 1.0 the site is /current/ alone: the newest full release of any
// line, with no line configured and no /X.Y/ path.
func TestBuildCurrentOnly(t *testing.T) {
	root, rels, hugo := siteRoot(t), releases(t), hugoBin(t)
	out := filepath.Join(t.TempDir(), "public")
	var log bytes.Buffer
	err := build(buildInput{
		root: root, out: out, baseURL: "https://docs.example/", publish: publishCurrent,
		src: dirSource{dir: rels}, hugo: hugo, log: &log,
	})
	if err != nil {
		t.Fatalf("build: %v\n%s", err, log.String())
	}

	ents, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range ents {
		got = append(got, e.Name())
	}
	if want := "404.html _redirects current fonts"; strings.Join(got, " ") != want {
		t.Errorf("the publish directory holds %v; want %s", got, want)
	}
	for _, p := range []string{"current/a/index.html", "current/commands/b/index.html", "current/new/index.html"} {
		if _, err := os.Stat(filepath.Join(out, p)); err != nil {
			t.Errorf("missing %s", p)
		}
	}
	if !strings.Contains(read(t, filepath.Join(out, "current", "a", "index.html")), "of 0.11.0") {
		t.Error("/current/ is not built from the newest release, v0.11.0")
	}
	if got := read(t, filepath.Join(out, "_redirects")); got != "/  /current/  301\n" {
		t.Errorf("_redirects = %q", got)
	}
	for page, want := range map[string]string{
		"current/index.html":     "https://docs.example/current/",
		"current/a/index.html":   "https://docs.example/current/a/",
		"current/new/index.html": "https://docs.example/current/new/",
	} {
		if got := canonical(t, filepath.Join(out, page)); got != want {
			t.Errorf("%s canonical = %s; want %s", page, got, want)
		}
	}
	page := read(t, filepath.Join(out, "current", "a", "index.html"))
	if strings.Contains(page, "rf-versions") || len(switcher(t, filepath.Join(out, "current", "a", "index.html"))) > 0 {
		t.Error("/current/a/ has a version switcher; want none with one release")
	}
	if !strings.Contains(page, "0.11.0") {
		t.Error("/current/a/ does not name its version, 0.11.0")
	}
	if strings.Contains(page, "noindex") {
		t.Error("a full release's /current/a/ has noindex")
	}
}

// Until the first full release, /current/ is the newest WIP's, and names
// that WIP's version.
func TestBuildCurrentFromWIP(t *testing.T) {
	root, hugo := siteRoot(t), hugoBin(t)
	rels := t.TempDir()
	for _, version := range []string{"0.10.0-wip.2", "0.10.0-wip.10"} {
		tree := t.TempDir()
		writeTree(t, tree, version, "0.10", []string{"a.md"})
		if err := pack(tree, "", rels, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "public")
	var log bytes.Buffer
	err := build(buildInput{
		root: root, out: out, baseURL: "https://docs.example/", publish: publishCurrent,
		src: dirSource{dir: rels}, hugo: hugo, log: &log,
	})
	if err != nil {
		t.Fatalf("build: %v\n%s", err, log.String())
	}
	page := read(t, filepath.Join(out, "current", "a", "index.html"))
	if !strings.Contains(page, "of 0.10.0-wip.10") {
		t.Error("/current/ is not built from the newest WIP, bin-v0.10.0-wip.10")
	}
	if !strings.Contains(page, ">0.10.0-wip.10<") {
		t.Error("the top bar does not name 0.10.0-wip.10")
	}
	if !regexp.MustCompile(`<meta name="?robots"? content="?noindex,nofollow"?>`).MatchString(page) {
		t.Error("a WIP's /current/a/ has no noindex")
	}
	if !strings.Contains(log.String(), "bin-v0.10.0-wip.10), a WIP") {
		t.Errorf("the log does not say /current/ is a WIP:\n%s", log.String())
	}
}

func TestBuildRefusesBadPublish(t *testing.T) {
	for _, in := range []buildInput{
		{publish: publishCurrent, lines: []string{"0.10"}},
		{publish: publishLines},
		{publish: "all", lines: []string{"0.10"}},
		{},
	} {
		in.root, in.out, in.baseURL, in.hugo, in.log = t.TempDir(), filepath.Join(t.TempDir(), "public"), "https://docs.example/", "hugo-is-not-reached", &bytes.Buffer{}
		in.src = dirSource{dir: t.TempDir()}
		if err := build(in); err == nil || !strings.Contains(err.Error(), "publish") {
			t.Errorf("build with publish %q, lines %v = %v; want a refusal naming publish", in.publish, in.lines, err)
		}
	}
}

func TestBuildRefusesChecksumMismatch(t *testing.T) {
	root, rels := siteRoot(t), releases(t)
	bundle := filepath.Join(rels, "v0.11.0", bundleName)
	body := []byte(read(t, bundle))
	body[len(body)/2] ^= 0xff
	if err := os.WriteFile(bundle, body, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "public")
	err := build(buildInput{
		root: root, out: out, baseURL: "https://docs.example/", publish: publishLines,
		lines: []string{"0.10", "0.11"}, src: dirSource{dir: rels}, hugo: "hugo-is-not-reached", log: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "refusing the bundle") || !strings.Contains(err.Error(), "v0.11.0") {
		t.Fatalf("build with a tampered bundle = %v; want a refusal naming v0.11.0", err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a refused build wrote the publish directory")
	}
}

func TestBuildRefusesManifestMismatch(t *testing.T) {
	root := siteRoot(t)
	rels := t.TempDir()
	tree := t.TempDir()
	writeTree(t, tree, "0.10.0", "0.10", []string{"a.md"})
	// Packed under the wrong tag: the release says 0.10.1, the bundle 0.10.0.
	if err := pack(tree, "bin-v0.10.1", rels, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	err := build(buildInput{
		root: root, out: filepath.Join(t.TempDir(), "public"), baseURL: "https://docs.example/", publish: publishLines,
		lines: []string{"0.10"}, src: dirSource{dir: rels}, hugo: "hugo-is-not-reached", log: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "manifest says") {
		t.Fatalf("build with a mismatched manifest = %v; want a refusal", err)
	}
}
