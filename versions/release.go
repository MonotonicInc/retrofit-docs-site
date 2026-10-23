package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// The asset names retrofit's release attaches (retrofit ma43).
const (
	bundleName    = "retrofit_docs.tar.gz"
	checksumsName = "checksums.txt"
)

// maxBundle caps what a bundle may expand to, so a bad one cannot fill
// the build machine.
const maxBundle = 256 << 20

type config struct {
	Repo    string   `toml:"repo"`
	Publish string   `toml:"publish"`
	Lines   []string `toml:"lines"`
}

// The publish modes. Before 1.0 the site is /current/ alone, the newest full
// release of any line; from 1.0 each listed line is built at /X.Y/ as well.
const (
	publishCurrent = "current"
	publishLines   = "lines"
)

var lineRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func loadConfig(file string) (config, error) {
	var cfg config
	md, err := toml.DecodeFile(file, &cfg)
	if err != nil {
		return cfg, err
	}
	if un := md.Undecoded(); len(un) > 0 {
		return cfg, fmt.Errorf("%s: unknown key %s", file, un[0])
	}
	if cfg.Repo == "" {
		return cfg, fmt.Errorf("%s: no repo", file)
	}
	switch cfg.Publish {
	case "":
		return cfg, fmt.Errorf("%s: no publish (%q or %q)", file, publishCurrent, publishLines)
	case publishCurrent:
		if len(cfg.Lines) > 0 {
			return cfg, fmt.Errorf("%s: publish = %q builds the newest release of any line; lines is for publish = %q", file, publishCurrent, publishLines)
		}
		return cfg, nil
	case publishLines:
		if len(cfg.Lines) == 0 {
			return cfg, fmt.Errorf("%s: publish = %q and no lines", file, publishLines)
		}
	default:
		return cfg, fmt.Errorf("%s: publish = %q; want %q or %q", file, cfg.Publish, publishCurrent, publishLines)
	}
	seen := map[string]bool{}
	for _, l := range cfg.Lines {
		if !lineRE.MatchString(l) {
			return cfg, fmt.Errorf("%s: line %q is not X.Y", file, l)
		}
		if seen[l] {
			return cfg, fmt.Errorf("%s: line %q listed twice", file, l)
		}
		seen[l] = true
	}
	return cfg, nil
}

// semver is a release's X.Y.Z, and for a WIP pre-release its N
// (X.Y.Z-wip.N); a full release has no N.
type semver struct {
	xyz [3]int
	wip int // 0 for a full release
}

func (v semver) line() string { return fmt.Sprintf("%d.%d", v.xyz[0], v.xyz[1]) }

func (v semver) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.xyz[0], v.xyz[1], v.xyz[2])
	if v.wip > 0 {
		s += fmt.Sprintf("-wip.%d", v.wip)
	}
	return s
}

// less orders as semver does: X.Y.Z-wip.N comes before X.Y.Z.
func (v semver) less(w semver) bool {
	if v.xyz != w.xyz {
		for i := range v.xyz {
			if v.xyz[i] != w.xyz[i] {
				return v.xyz[i] < w.xyz[i]
			}
		}
	}
	switch {
	case v.wip == w.wip:
		return false
	case v.wip == 0:
		return false
	case w.wip == 0:
		return true
	}
	return v.wip < w.wip
}

// tagRE is a release tag: bin-vX.Y.Z until the source release, vX.Y.Z
// after, and a WIP pre-release's X.Y.Z-wip.N. Any other suffix does not
// match.
var tagRE = regexp.MustCompile(`^(?:bin-)?v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-wip\.([1-9][0-9]*))?$`)

func parseTag(tag string) (semver, bool) {
	m := tagRE.FindStringSubmatch(tag)
	if m == nil {
		return semver{}, false
	}
	var v semver
	for i := range v.xyz {
		v.xyz[i], _ = strconv.Atoi(m[i+1])
	}
	if m[4] != "" {
		v.wip, _ = strconv.Atoi(m[4])
	}
	return v, true
}

// anyLine asks a source for the newest full release whatever its line, or
// the newest WIP while there is no full release.
const anyLine = ""

// lineName is how a refusal names what was asked for.
func lineName(line string) string {
	if line == anyLine {
		return "the newest release"
	}
	return "line " + line
}

// newestTag picks the newest full release of line among tags. For anyLine
// it picks the newest full release of any line, and only when there is
// none, the newest WIP: before the first full release the site publishes a
// WIP's docs, and from then on WIPs are ignored with no edit anywhere.
func newestTag(line string, tags []string) (string, semver, bool) {
	newest := func(wip bool) (string, semver, bool) {
		var best string
		var bestV semver
		for _, t := range tags {
			v, ok := parseTag(t)
			if !ok || (v.wip > 0) != wip || (line != anyLine && v.line() != line) {
				continue
			}
			if best == "" || bestV.less(v) {
				best, bestV = t, v
			}
		}
		return best, bestV, best != ""
	}
	if tag, v, ok := newest(false); ok || line != anyLine {
		return tag, v, ok
	}
	return newest(true)
}

// A source finds a line's newest full release (or, for anyLine, what
// newestTag picks) and reads its assets.
type source interface {
	newest(line string) (tag string, v semver, err error)
	asset(tag, name string) ([]byte, error)
}

// githubSource reads the repository's releases from GitHub with no token
// and no API call: the release tags from the repository's git refs
// (<dl>/<repo>.git/info/refs, the public listing `git ls-remote --tags`
// reads), the assets from releases/download/<tag>/. retrofit's publish
// uploads into a draft, which holds no tag, and publishes it last
// (.github/scripts/release-publish.sh), so a public tag stands for a
// published release with its assets; one missing an asset fails the build
// on the download. Nothing here needs GitHub's API or its rate limit. The
// refs are read once per build, however many lines there are.
type githubSource struct {
	repo, dl string
	client   *http.Client

	tags   []string // release tags, once listed
	listed bool
}

func httpClient() *http.Client { return &http.Client{Timeout: 2 * time.Minute} }

func (g *githubSource) list() ([]string, error) {
	if g.listed {
		return g.tags, nil
	}
	url := fmt.Sprintf("%s/%s.git/info/refs?service=git-upload-pack", g.dl, g.repo)
	body, err := g.get(url)
	if err != nil {
		return nil, err
	}
	refs, err := parseRefs(body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	var tags []string
	for _, ref := range refs {
		if tag, ok := strings.CutPrefix(ref, "refs/tags/"); ok {
			if _, ok := parseTag(tag); ok {
				tags = append(tags, tag)
			}
		}
	}
	g.tags, g.listed = tags, true
	return tags, nil
}

// parseRefs reads the ref names from a smart HTTP ref advertisement: a
// service line and a flush, then one pkt-line per ref ("<sha> <ref>", the
// first followed by NUL and the capabilities), then a flush. A peeled tag
// ("<ref>^{}") is skipped; it repeats its tag.
func parseRefs(body []byte) ([]string, error) {
	var refs []string
	flushes := 0
	for len(body) > 0 {
		if len(body) < 4 {
			return nil, errors.New("truncated pkt-line")
		}
		n, err := strconv.ParseUint(string(body[:4]), 16, 16)
		if err != nil {
			return nil, fmt.Errorf("bad pkt-line length %q", body[:4])
		}
		if n == 0 {
			body = body[4:]
			if flushes++; flushes == 2 {
				return refs, nil
			}
			continue
		}
		if n < 4 || int(n) > len(body) {
			return nil, errors.New("truncated pkt-line")
		}
		line := strings.TrimSuffix(string(body[4:n]), "\n")
		body = body[n:]
		if strings.HasPrefix(line, "# service=") {
			continue
		}
		line, _, _ = strings.Cut(line, "\x00")
		_, ref, ok := strings.Cut(line, " ")
		if !ok {
			return nil, fmt.Errorf("bad ref line %q", line)
		}
		if !strings.HasSuffix(ref, "^{}") {
			refs = append(refs, ref)
		}
	}
	return nil, errors.New("no closing flush")
}

func (g *githubSource) newest(line string) (string, semver, error) {
	tags, err := g.list()
	if err != nil {
		return "", semver{}, err
	}
	tag, v, ok := newestTag(line, tags)
	if !ok {
		return "", semver{}, fmt.Errorf("%s: no release on %s", lineName(line), g.repo)
	}
	return tag, v, nil
}

func (g *githubSource) asset(tag, name string) ([]byte, error) {
	return g.get(fmt.Sprintf("%s/%s/releases/download/%s/%s", g.dl, g.repo, tag, name))
}

func (g *githubSource) get(url string) ([]byte, error) {
	resp, err := g.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBundle+1))
}

// dirSource reads releases from <dir>/<tag>/, one directory per release.
type dirSource struct{ dir string }

func (d dirSource) newest(line string) (string, semver, error) {
	ents, err := os.ReadDir(d.dir)
	if err != nil {
		return "", semver{}, err
	}
	var tags []string
	for _, e := range ents {
		if e.IsDir() {
			tags = append(tags, e.Name())
		}
	}
	tag, v, ok := newestTag(line, tags)
	if !ok {
		return "", semver{}, fmt.Errorf("%s: no release in %s", lineName(line), d.dir)
	}
	return tag, v, nil
}

func (d dirSource) asset(tag, name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(d.dir, tag, name))
}

// verify checks bundle against the one line checksums.txt gives it.
func verify(bundle, checksums []byte) error {
	var want string
	n := 0
	sc := bufio.NewScanner(bytes.NewReader(checksums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == bundleName {
			want = strings.ToLower(f[0])
			n++
		}
	}
	if n != 1 {
		return fmt.Errorf("%s names %s %d times; want once", checksumsName, bundleName, n)
	}
	sum := sha256.Sum256(bundle)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("%s: sha256 %s, but %s says %s", bundleName, got, checksumsName, want)
	}
	return nil
}

// manifest is the part of the bundle's manifest.json the build reads.
type manifest struct {
	Version  string `json:"version"`
	Line     string `json:"line"`
	Sections []struct {
		Pages []struct {
			Path string `json:"path"`
		} `json:"pages"`
	} `json:"sections"`
}

func (m manifest) pages() []string {
	var ps []string
	for _, s := range m.Sections {
		for _, p := range s.Pages {
			ps = append(ps, p.Path)
		}
	}
	return ps
}

// extract unpacks a bundle into dir, which must not exist, and reads its
// manifest. Only regular files and directories under dir are accepted.
func extract(bundle []byte, dir string) (manifest, error) {
	var m manifest
	if _, err := os.Stat(dir); err == nil {
		return m, fmt.Errorf("%s exists", dir)
	}
	zr, err := gzip.NewReader(bytes.NewReader(bundle))
	if err != nil {
		return m, fmt.Errorf("%s: %w", bundleName, err)
	}
	tr := tar.NewReader(zr)
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return m, fmt.Errorf("%s: %w", bundleName, err)
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		if name == "." {
			continue
		}
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return m, fmt.Errorf("%s: entry %q leaves the bundle", bundleName, h.Name)
		}
		dst := filepath.Join(dir, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return m, err
			}
		case tar.TypeReg:
			total += h.Size
			if total > maxBundle {
				return m, fmt.Errorf("%s: expands past %d bytes", bundleName, maxBundle)
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return m, err
			}
			f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err != nil {
				return m, err
			}
			_, err = io.Copy(f, io.LimitReader(tr, h.Size))
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return m, err
			}
		default:
			return m, fmt.Errorf("%s: entry %q is not a file or directory", bundleName, h.Name)
		}
	}
	body, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return m, fmt.Errorf("%s: no manifest.json", bundleName)
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return m, fmt.Errorf("%s: manifest.json: %w", bundleName, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "content")); err != nil || !fi.IsDir() {
		return m, fmt.Errorf("%s: no content/", bundleName)
	}
	return m, nil
}
