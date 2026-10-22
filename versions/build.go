package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type buildInput struct {
	root    string // the site repository: hugo.toml, layouts, static
	out     string // the publish directory, replaced whole
	baseURL string
	publish string   // publishCurrent or publishLines
	lines   []string // publishLines: the lines to build at /X.Y/
	src     source
	hugo    string
	log     io.Writer
}

type lineBuild struct {
	line, tag string
	v         semver
	dir       string
	m         manifest
}

// versionsData is _tree/versions.json, which each Hugo build reads as
// hugo.Data.versions: the canonical (layouts/_partials/canonical.html) and
// the version switcher (layouts/_partials/topbar.html).
type versionsData struct {
	Self       string         `json:"self"`       // the path being built: a line, or "current"
	Current    string         `json:"current"`    // the newest line
	CurrentURL string         `json:"currentURL"` // absolute URL of /current/
	Lines      []versionsLine `json:"lines"`      // newest first
}

type versionsLine struct {
	Line    string   `json:"line"`
	Version string   `json:"version"`
	URL     string   `json:"url"` // root-relative: /X.Y/
	Pages   []string `json:"pages"`
}

func build(in buildInput) error {
	base, err := url.Parse(in.baseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return fmt.Errorf("-base-url %q is not an absolute URL", in.baseURL)
	}
	if !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
	}
	switch {
	case in.publish == publishCurrent && len(in.lines) > 0:
		return fmt.Errorf("publish %q takes no lines", publishCurrent)
	case in.publish == publishLines && len(in.lines) == 0:
		return fmt.Errorf("publish %q and no lines", publishLines)
	case in.publish != publishCurrent && in.publish != publishLines:
		return fmt.Errorf("publish %q; want %q or %q", in.publish, publishCurrent, publishLines)
	}

	// Fetch, check and unpack every release before building anything, so a
	// refusal leaves no partial site behind.
	work := filepath.Join(in.root, "_versions")
	if err := os.RemoveAll(work); err != nil {
		return err
	}
	// Before 1.0 (publishCurrent) one release is built, the newest of any
	// line, and only at /current/. From 1.0 (publishLines) each listed
	// line's newest release is built at /X.Y/ and the newest again there.
	asks := in.lines
	if in.publish == publishCurrent {
		asks = []string{anyLine}
	}
	var builds []lineBuild
	for _, ask := range asks {
		tag, v, err := in.src.newest(ask)
		if err != nil {
			return err
		}
		line := v.line()
		bundle, err := in.src.asset(tag, bundleName)
		if err != nil {
			return fmt.Errorf("line %s: %s: %w", line, tag, err)
		}
		sums, err := in.src.asset(tag, checksumsName)
		if err != nil {
			return fmt.Errorf("line %s: %s: %w", line, tag, err)
		}
		if err := verify(bundle, sums); err != nil {
			return fmt.Errorf("line %s: %s: refusing the bundle: %w", line, tag, err)
		}
		dir := filepath.Join(work, line)
		m, err := extract(bundle, dir)
		if err != nil {
			return fmt.Errorf("line %s: %s: %w", line, tag, err)
		}
		if m.Line != line || m.Version != v.String() {
			return fmt.Errorf("line %s: %s: the bundle's manifest says version %q, line %q", line, tag, m.Version, m.Line)
		}
		fmt.Fprintf(in.log, "versions: line %s: %s, %s checked against %s\n", line, tag, bundleName, checksumsName)
		builds = append(builds, lineBuild{line: line, tag: tag, v: v, dir: dir, m: m})
	}
	sort.Slice(builds, func(i, j int) bool { return builds[j].v.less(builds[i].v) })
	current := builds[0]

	data := versionsData{
		Current:    current.line,
		CurrentURL: base.JoinPath("current").String() + "/",
	}
	for _, b := range builds {
		url := path.Join(base.Path, b.line) + "/"
		if in.publish == publishCurrent {
			url = path.Join(base.Path, "current") + "/"
		}
		data.Lines = append(data.Lines, versionsLine{
			Line:    b.line,
			Version: b.m.Version,
			URL:     url,
			Pages:   b.m.pages(),
		})
	}

	out, err := filepath.Abs(in.out)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	type target struct {
		name string
		b    lineBuild
	}
	targets := []target{}
	if in.publish == publishLines {
		for _, b := range builds {
			targets = append(targets, target{b.line, b})
		}
	}
	targets = append(targets, target{"current", current})

	tree := filepath.Join(in.root, "_tree")
	for _, t := range targets {
		if err := os.RemoveAll(tree); err != nil {
			return err
		}
		if err := copyDir(t.b.dir, tree); err != nil {
			return err
		}
		d := data
		d.Self = t.name
		body, err := json.MarshalIndent(d, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(tree, "versions.json"), body, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(in.log, "versions: building /%s/ from %s\n", t.name, t.b.tag)
		cmd := exec.Command(in.hugo, "--gc", "--minify",
			"--source", in.root,
			"--baseURL", base.JoinPath(t.name).String()+"/",
			"--destination", filepath.Join(out, t.name))
		cmd.Stdout, cmd.Stderr = in.log, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("hugo for /%s/: %w", t.name, err)
		}
	}

	// The root. fonts.css loads the faces from /fonts/ at the site root,
	// whichever path the page is under, so the fonts live there once.
	if err := copyDir(filepath.Join(in.root, "static", "fonts"), filepath.Join(out, "fonts")); err != nil {
		return err
	}
	redirects := fmt.Sprintf("%s  %s  301\n", base.Path, data.CurrentURL[len(base.Scheme)+3+len(base.Host):])
	if err := os.WriteFile(filepath.Join(out, "_redirects"), []byte(redirects), 0o644); err != nil {
		return err
	}
	notFound, err := os.ReadFile(filepath.Join(out, "current", "404.html"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "404.html"), notFound, 0o644); err != nil {
		return err
	}
	if in.publish == publishCurrent {
		fmt.Fprintf(in.log, "versions: /current/ only, %s (%s), the newest full release\n", current.line, current.tag)
		return nil
	}
	lines := "lines"
	if len(builds) == 1 {
		lines = "line"
	}
	fmt.Fprintf(in.log, "versions: %d %s, /current/ is %s (%s)\n", len(builds), lines, current.line, current.tag)
	return nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", p)
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(to, body, 0o644)
	})
}
