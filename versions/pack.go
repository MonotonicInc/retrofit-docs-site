package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// pack writes <out>/<tag>/retrofit_docs.tar.gz from tree, with the
// checksums.txt line a release carries for it. The archive is
// deterministic: sorted entries, fixed times, modes and owners. With no
// tag, it is bin-v and the version the tree's manifest names.
func pack(tree, tag, out string, log io.Writer) error {
	if tag == "" {
		body, err := os.ReadFile(filepath.Join(tree, "manifest.json"))
		if err != nil {
			return err
		}
		var m manifest
		if err := json.Unmarshal(body, &m); err != nil {
			return fmt.Errorf("%s/manifest.json: %w", tree, err)
		}
		tag = "bin-v" + m.Version
	}
	if _, ok := parseTag(tag); !ok {
		return fmt.Errorf("-tag %q is not a full release tag (bin-vX.Y.Z or vX.Y.Z)", tag)
	}
	bundle, err := tarTree(tree)
	if err != nil {
		return err
	}
	dir := filepath.Join(out, tag)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, bundleName), bundle, 0o644); err != nil {
		return err
	}
	sum := sha256.Sum256(bundle)
	line := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), bundleName)
	if err := os.WriteFile(filepath.Join(dir, checksumsName), []byte(line), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(log, "versions: packed %s into %s\n", tree, dir)
	return nil
}

func tarTree(tree string) ([]byte, error) {
	var names []string
	err := filepath.WalkDir(tree, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == tree {
			return nil
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", p)
		}
		names = append(names, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	epoch := time.Unix(0, 0).UTC()
	for _, p := range names {
		rel, err := filepath.Rel(tree, p)
		if err != nil {
			return nil, err
		}
		fi, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		h := &tar.Header{Name: filepath.ToSlash(rel), ModTime: epoch, Format: tar.FormatPAX}
		if fi.IsDir() {
			h.Typeflag, h.Name, h.Mode = tar.TypeDir, h.Name+"/", 0o755
			if err := tw.WriteHeader(h); err != nil {
				return nil, err
			}
			continue
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		h.Typeflag, h.Mode, h.Size = tar.TypeReg, 0o644, int64(len(body))
		if err := tw.WriteHeader(h); err != nil {
			return nil, err
		}
		if _, err := tw.Write(body); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
