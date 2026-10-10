package main

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// openTree finds the manifest tree an argument names, and returns the directory to scan and how to
// clean up after it. An existing directory is itself. Otherwise the argument is [REMOTE#]REF:PATH:
// PATH in commit REF of the repository the command runs in or, with REMOTE, of that remote (a remote
// name, a URL or a path), fetched into a temporary repository so the checkout is left alone. The
// commit's PATH is unpacked into a temporary directory, which the clean up removes.
func openTree(ctx context.Context, arg string) (string, func() error, error) {
	if info, err := os.Stat(arg); err == nil && info.IsDir() {
		return arg, func() error { return nil }, nil
	}
	treeish, remote := arg, ""
	if before, after, found := strings.Cut(arg, "#"); found {
		treeish, remote = after, before
	}
	ref, path, found := strings.Cut(treeish, ":")
	if !found || ref == "" {
		return "", nil, errors.New("not a directory, and not [REMOTE#]REF:PATH")
	}

	tmp, err := os.MkdirTemp("", "apiexport-pie-")
	if err != nil {
		return "", nil, fmt.Errorf("making a directory to unpack into: %w", err)
	}
	cleanup := func() error { return os.RemoveAll(tmp) }
	repo := "."
	if remote != "" {
		repo = filepath.Join(tmp, "repo.git")
		if err := fetch(ctx, remote, ref, repo); err != nil {
			return "", nil, errors.Join(err, cleanup())
		}
		treeish = "FETCH_HEAD:" + path
	}
	tree := filepath.Join(tmp, "tree")
	if err := archive(ctx, repo, treeish, tree); err != nil {
		return "", nil, errors.Join(err, cleanup())
	}
	return tree, cleanup, nil
}

// fetch fetches ref of remote, one commit deep, into a new bare repository. A remote of the
// repository the command runs in is fetched from its URL; anything else is taken as a URL or path.
func fetch(ctx context.Context, remote, ref, repo string) error {
	url := remote
	if out, err := gitOutput(ctx, ".", "remote", "get-url", remote); err == nil {
		url = strings.TrimSpace(out)
	}
	if _, err := gitOutput(ctx, ".", "init", "--quiet", "--bare", repo); err != nil {
		return err
	}
	_, err := gitOutput(ctx, repo, "fetch", "--quiet", "--depth=1", "--no-tags", url, ref)
	return err
}

// archive unpacks the files of treeish into dir. Only regular files are written, and only inside dir.
func archive(ctx context.Context, repo, treeish, dir string) error {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "archive", "--format=tar", treeish)
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("git archive: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("git archive: %w", err)
	}
	unpacked := unpack(out, dir)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git archive: %s", strings.TrimSpace(stderr.String()))
	}
	return unpacked
}

func unpack(r io.Reader, dir string) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading the archive: %w", err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.FromSlash(h.Name)
		if !filepath.IsLocal(name) {
			return fmt.Errorf("archive entry %s is outside the tree", h.Name)
		}
		if err := writeFile(filepath.Join(dir, name), tr); err != nil {
			return err
		}
	}
}

func writeFile(path string, content io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("unpacking: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("unpacking: %w", err)
	}
	if _, err := io.Copy(f, content); err != nil {
		return errors.Join(fmt.Errorf("unpacking %s: %w", path, err), f.Close())
	}
	return f.Close()
}

// gitOutput runs git in dir and returns its output, or an error carrying what git said.
func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if said := strings.TrimSpace(stderr.String()); said != "" {
			return "", fmt.Errorf("git %s: %s", args[0], said)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return string(out), nil
}
