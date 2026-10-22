// Command versions builds the versioned docs site: one Hugo build per
// supported release line at /X.Y/, the newest line again at /current/, and
// the root files (a redirect to /current/, the fonts, a 404 page).
//
//	versions build -root <site> -out <dir> [-config versions.toml]
//	               [-base-url <url>] [-releases-dir <dir>] [-hugo <path>]
//	versions pack -tree <dir> -out <dir> [-tag <tag>]
//
// build reads each line's docs bundle (retrofit_docs.tar.gz) from the newest
// full release of that line, checks it against the release's checksums.txt
// and refuses on a mismatch. By default the releases are the repository's on
// GitHub; -releases-dir reads them from <dir>/<tag>/ instead, laid out as
// releases/download/<tag>/ is, which is how a build runs before a release
// exists and how the tests run.
//
// pack writes a release-shaped fixture, <out>/<tag>/retrofit_docs.tar.gz and
// its checksums.txt, from a tree retrofit's `go run ./cmd/docs` wrote. The
// tag defaults to bin-v and the version in the tree's manifest.
//
// A refusal prints its reason and exits 1; a usage error exits 2.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

const usage = `Usage:
  versions build -root <site> -out <dir> [-config versions.toml] [-base-url <url>] [-releases-dir <dir>] [-hugo <path>]
  versions pack -tree <dir> -out <dir> [-tag <tag>]
`

var errUsage = errors.New("usage")

func main() {
	err := run(os.Args[1:], os.Stdout)
	if errors.Is(err, errUsage) {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "versions:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	switch args[0] {
	case "build":
		root := fs.String("root", ".", "")
		out := fs.String("out", "", "")
		config := fs.String("config", "", "")
		baseURL := fs.String("base-url", "https://docs.retrofit.sh/", "")
		releasesDir := fs.String("releases-dir", "", "")
		hugo := fs.String("hugo", "hugo", "")
		if fs.Parse(args[1:]) != nil || fs.NArg() != 0 || *out == "" {
			return errUsage
		}
		if *config == "" {
			*config = *root + "/versions.toml"
		}
		cfg, err := loadConfig(*config)
		if err != nil {
			return err
		}
		var src source = &githubSource{
			repo:   cfg.Repo,
			api:    "https://api.github.com",
			dl:     "https://github.com",
			token:  os.Getenv("GITHUB_TOKEN"),
			client: httpClient(),
		}
		if *releasesDir != "" {
			src = dirSource{dir: *releasesDir}
		}
		return build(buildInput{
			root:    *root,
			out:     *out,
			baseURL: *baseURL,
			lines:   cfg.Lines,
			src:     src,
			hugo:    *hugo,
			log:     stdout,
		})
	case "pack":
		tree := fs.String("tree", "", "")
		tag := fs.String("tag", "", "")
		out := fs.String("out", "", "")
		if fs.Parse(args[1:]) != nil || fs.NArg() != 0 || *tree == "" || *out == "" {
			return errUsage
		}
		return pack(*tree, *tag, *out, stdout)
	}
	return errUsage
}
