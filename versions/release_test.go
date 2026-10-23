package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNewestTag(t *testing.T) {
	t.Parallel()
	tags := []string{
		"bin-v0.10.0", "bin-v0.10.2", "bin-v0.10.10", "v0.10.3",
		"bin-v0.10.11-wip.4", "v0.11.0", "v0.1.99", "docs-v0.10.99", "bin-v00.10.50",
	}
	for line, want := range map[string]string{"0.10": "bin-v0.10.10", "0.11": "v0.11.0", "0.1": "v0.1.99"} {
		if got, _, ok := newestTag(line, tags); !ok || got != want {
			t.Errorf("newestTag(%s) = %q, %v; want %q", line, got, ok, want)
		}
	}
	if got, _, ok := newestTag("1.0", tags); ok {
		t.Errorf("newestTag(1.0) = %q; want none", got)
	}
	if got, _, ok := newestTag(anyLine, tags); !ok || got != "v0.11.0" {
		t.Errorf("newestTag(any line) = %q, %v; want v0.11.0", got, ok)
	}

	// A WIP only while no full release exists, and then the newest by N.
	wips := []string{"bin-v0.10.0-wip.2", "bin-v0.10.0-wip.10", "bin-v0.9.9-wip.30", "bin-v0.10.0-rc.1", "bin-v0.10.0-wip.0"}
	if got, v, ok := newestTag(anyLine, wips); !ok || got != "bin-v0.10.0-wip.10" || v.String() != "0.10.0-wip.10" || v.line() != "0.10" {
		t.Errorf("newestTag(any line) over WIPs = %q, %v, %v; want bin-v0.10.0-wip.10", got, v, ok)
	}
	if got, _, ok := newestTag("0.10", wips); ok {
		t.Errorf("newestTag(0.10) over WIPs = %q; want none (a line takes full releases only)", got)
	}
	if got, _, ok := newestTag(anyLine, append(wips, "bin-v0.9.0", "bin-v0.11.0-wip.1")); !ok || got != "bin-v0.9.0" {
		t.Errorf("newestTag(any line) with one full release = %q, %v; want bin-v0.9.0, the WIPs ignored", got, ok)
	}
}

func TestSemverOrder(t *testing.T) {
	t.Parallel()
	order := []string{"v0.9.9", "v0.10.0-wip.1", "v0.10.0-wip.2", "v0.10.0-wip.10", "v0.10.0", "v0.10.1-wip.1", "v0.10.1", "v1.0.0"}
	for i := range order {
		for j := range order {
			a, _ := parseTag(order[i])
			b, _ := parseTag(order[j])
			if got := a.less(b); got != (i < j) {
				t.Errorf("%s less %s = %v; want %v", order[i], order[j], got, i < j)
			}
		}
	}
}

func TestLoadConfig(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, body, refusal string
	}{
		{"current", "repo = \"o/r\"\npublish = \"current\"\n", ""},
		{"lines", "repo = \"o/r\"\npublish = \"lines\"\nlines = [\"0.10\", \"1.0\"]\n", ""},
		{"current with lines", "repo = \"o/r\"\npublish = \"current\"\nlines = [\"0.10\"]\n", "lines is for"},
		{"lines without lines", "repo = \"o/r\"\npublish = \"lines\"\n", "no lines"},
		{"unknown publish", "repo = \"o/r\"\npublish = \"all\"\n", "want \"current\" or \"lines\""},
		{"no publish", "repo = \"o/r\"\nlines = [\"0.10\"]\n", "no publish"},
		{"no repo", "publish = \"current\"\n", "no repo"},
		{"unknown key", "repo = \"o/r\"\npublish = \"current\"\nline = \"0.10\"\n", "unknown key"},
		{"bad line", "repo = \"o/r\"\npublish = \"lines\"\nlines = [\"v0.10\"]\n", "not X.Y"},
		{"line twice", "repo = \"o/r\"\npublish = \"lines\"\nlines = [\"0.10\", \"0.10\"]\n", "twice"},
	} {
		f := filepath.Join(t.TempDir(), "versions.toml")
		if err := os.WriteFile(f, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := loadConfig(f)
		switch {
		case c.refusal == "" && err != nil:
			t.Errorf("%s: %v; want it accepted", c.name, err)
		case c.refusal != "" && (err == nil || !strings.Contains(err.Error(), c.refusal)):
			t.Errorf("%s: %v; want a refusal containing %q", c.name, err, c.refusal)
		}
	}
}

func TestGithubSourceSkipsDraftsAndPrereleases(t *testing.T) {
	t.Parallel()
	page1 := make([]map[string]any, 100)
	for i := range page1 {
		page1[i] = map[string]any{"tag_name": fmt.Sprintf("bin-v0.9.%d", i)}
	}
	page2 := []map[string]any{
		{"tag_name": "bin-v0.10.1"},
		{"tag_name": "bin-v0.10.2", "prerelease": true},
		{"tag_name": "bin-v0.10.3", "draft": true},
		{"tag_name": "bin-v0.11.0-wip.1", "prerelease": true},
		{"tag_name": "bin-v0.11.0-wip.2"},
		{"tag_name": "bin-v0.11.0-wip.3", "prerelease": true, "draft": true},
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/repos/o/r/releases" {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Query().Get("page") {
		case "1":
			_ = json.NewEncoder(w).Encode(page1)
		case "2":
			_ = json.NewEncoder(w).Encode(page2)
		default:
			_ = json.NewEncoder(w).Encode([]any{})
		}
	}))
	defer srv.Close()
	g := &githubSource{repo: "o/r", api: srv.URL, dl: srv.URL, client: srv.Client()}
	for line, want := range map[string]string{"0.10": "bin-v0.10.1", "0.9": "bin-v0.9.99"} {
		if got, _, err := g.newest(line); err != nil || got != want {
			t.Errorf("newest(%s) = %q, %v; want %q", line, got, err, want)
		}
	}
	if _, _, err := g.newest("0.11"); err == nil {
		t.Error("newest(0.11) found a release; want a refusal")
	}
	if got, _, err := g.newest(anyLine); err != nil || got != "bin-v0.10.1" {
		t.Errorf("newest(any line) = %q, %v; want bin-v0.10.1", got, err)
	}
	// The WIP the list kept: a pre-release, not a draft.
	if got := strings.Join(g.tags[len(g.tags)-2:], " "); got != "bin-v0.10.1 bin-v0.11.0-wip.1" {
		t.Errorf("the list ends %q; want bin-v0.10.1 bin-v0.11.0-wip.1", got)
	}
	// Four lookups, two pages of releases: the list is fetched once.
	if n := calls.Load(); n != 2 {
		t.Errorf("%d API calls for four lookups over two pages; want 2", n)
	}
}

func checksumsFor(bundle []byte) []byte {
	sum := sha256.Sum256(bundle)
	return []byte(fmt.Sprintf("aaaa  retrofit_linux_amd64.tar.gz\n%s  %s\n", hex.EncodeToString(sum[:]), bundleName))
}

func TestVerify(t *testing.T) {
	t.Parallel()
	bundle := []byte("bundle")
	if err := verify(bundle, checksumsFor(bundle)); err != nil {
		t.Fatalf("verify of a matching bundle: %v", err)
	}
	if err := verify([]byte("tampered"), checksumsFor(bundle)); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Errorf("verify of a mismatched bundle = %v; want a sha256 refusal", err)
	}
	if err := verify(bundle, []byte("aaaa  retrofit_linux_amd64.tar.gz\n")); err == nil {
		t.Error("verify with no line for the bundle accepted it")
	}
	twice := append(checksumsFor(bundle), checksumsFor(bundle)...)
	if err := verify(bundle, twice); err == nil {
		t.Error("verify with two lines for the bundle accepted it")
	}
}

// writeTree writes a minimal docs tree: a manifest naming pages, and each
// page with front matter, as retrofit's generator writes them.
func writeTree(t *testing.T, dir, version, line string, pages []string) {
	t.Helper()
	type page struct {
		Path  string `json:"path"`
		Title string `json:"title"`
	}
	var ps []page
	for i, p := range pages {
		title := strings.TrimSuffix(filepath.Base(p), ".md")
		ps = append(ps, page{p, title})
		body := fmt.Sprintf("---\ntitle: %s\nweight: %d\n---\n\n# %s\n\nThe %s page of %s.\n\n## A section\n\nText.\n", title, i+1, title, title, version)
		f := filepath.Join(dir, "content", filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := map[string]any{"version": version, "line": line, "sections": []any{map[string]any{"title": "Pages", "pages": ps}}}
	body, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPackExtractRoundTrip(t *testing.T) {
	t.Parallel()
	tree := t.TempDir()
	writeTree(t, tree, "0.10.0", "0.10", []string{"a.md", "commands/b.md"})
	out := t.TempDir()
	if err := pack(tree, "bin-v0.10.0", out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	bundle, _ := os.ReadFile(filepath.Join(out, "bin-v0.10.0", bundleName))
	sums, _ := os.ReadFile(filepath.Join(out, "bin-v0.10.0", checksumsName))
	if err := verify(bundle, sums); err != nil {
		t.Fatalf("pack's checksums.txt does not verify its bundle: %v", err)
	}
	again, _ := tarTree(tree)
	if !bytes.Equal(bundle, again) {
		t.Error("packing the same tree twice gave different bytes")
	}
	dst := filepath.Join(t.TempDir(), "x")
	m, err := extract(bundle, dst)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "0.10.0" || m.Line != "0.10" || strings.Join(m.pages(), ",") != "a.md,commands/b.md" {
		t.Errorf("extracted manifest = %+v", m)
	}
	if _, err := os.Stat(filepath.Join(dst, "content", "commands", "b.md")); err != nil {
		t.Error(err)
	}
}

func TestExtractRefusesEscape(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"../x", "/etc/x", "content/../../x"} {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(zw)
		_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
		_, _ = tw.Write([]byte("x"))
		_ = tw.Close()
		_ = zw.Close()
		if _, err := extract(buf.Bytes(), filepath.Join(t.TempDir(), "dst")); err == nil || !strings.Contains(err.Error(), "leaves the bundle") {
			t.Errorf("extract of %q = %v; want a refusal", name, err)
		}
	}
}
