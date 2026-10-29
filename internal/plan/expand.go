// Package plan expands remote selections into safe local download targets.
package plan

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"spiid/internal/errs"
	"spiid/internal/sftp"
)

// Item is a regular file selected for download.
type Item struct {
	Remote  string
	Local   string
	Size    int64
	ModTime time.Time
}

// Skipped records a remote entry excluded from the plan.
type Skipped struct{ Remote, Reason string }

type walker struct {
	emit        func(Item)
	onSkip      func(Skipped)
	depth       int
	count       int
	ctx         context.Context
	c           *sftp.Client
	downloadDir string
	items       []Item
	skipped     []Skipped
	seen        map[string]string
	dirs        map[string]string
}

// Expand expands roots in order, preserving each root's tree beneath downloadDir.
func Expand(ctx context.Context, c *sftp.Client, roots []string, downloadDir string) ([]Item, []Skipped, error) {
	return expand(ctx, c, roots, downloadDir, nil, nil)
}

// ExpandEach queues each discovered file immediately.
func ExpandEach(ctx context.Context, c *sftp.Client, roots []string, downloadDir string,
	emit func(Item), skip func(Skipped),
) error {
	_, _, err := expand(ctx, c, roots, downloadDir, emit, skip)
	return err
}

func expand(ctx context.Context, c *sftp.Client, roots []string, downloadDir string,
	emit func(Item), skip func(Skipped),
) ([]Item, []Skipped, error) {
	if !filepath.IsAbs(downloadDir) || filepath.Clean(downloadDir) != downloadDir {
		return nil, nil, errs.New(errs.InvalidInput, "expand", downloadDir, nil)
	}
	w := walker{emit: emit, onSkip: skip,
		ctx:         ctx,
		c:           c,
		downloadDir: downloadDir,
		seen:        make(map[string]string),
		dirs:        make(map[string]string),
	}
	for _, root := range roots {
		if w.count >= 1_000_000 {
			return nil, nil, fmt.Errorf("scan item limit 1000000 reached")
		}
		if err := w.canceled(); err != nil {
			return nil, nil, err
		}
		if !path.IsAbs(root) {
			return nil, nil, errs.New(errs.InvalidInput, "expand", root, nil)
		}
		root = path.Clean(root)
		info, err := c.Stat(ctx, root)
		if err != nil {
			if ctx.Err() != nil {
				err = w.canceled()
			}
			return nil, nil, err
		}
		if root != "/" && !SafeName(path.Base(root)) {
			w.skip(root, "unsafe name")
			continue
		}
		if err := w.visit(root, path.Dir(root), info); err != nil {
			return nil, nil, err
		}
	}
	if err := w.canceled(); err != nil {
		return nil, nil, err
	}
	return w.items, w.skipped, nil
}

func (w *walker) canceled() error {
	if w.ctx.Err() != nil {
		return errs.New(errs.Canceled, "expand", "", w.ctx.Err())
	}
	return nil
}

func (w *walker) skip(remote, reason string) {
	w.count++
	if w.onSkip != nil {
		w.onSkip(Skipped{remote, reason})
	} else {
		w.skipped = append(w.skipped, Skipped{remote, reason})
	}
}

func (w *walker) visit(remote, parent string, info sftp.FileInfo) error {
	if err := w.canceled(); err != nil {
		return err
	}
	if w.count >= 1_000_000 {
		return fmt.Errorf("scan item limit 1000000 reached")
	}
	if w.depth >= 256 {
		return fmt.Errorf("scan depth limit 256 reached")
	}
	w.depth++
	defer func() { w.depth-- }()
	name := strings.ToLower(path.Base(remote))
	if strings.HasSuffix(name, ".spiid-part") || strings.HasSuffix(name, ".spiid-state") {
		w.skip(remote, "reserved name")
		return nil
	}
	if info.IsDir() {
		entries, err := w.c.ReadDir(w.ctx, remote)
		if err != nil {
			if w.ctx.Err() != nil {
				return w.canceled()
			}
			if errs.KindOf(err) == errs.Protocol {
				return err
			}
			w.skip(remote, "can't list: "+err.Error())
			return nil
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		for _, entry := range entries {
			if err := w.canceled(); err != nil {
				return err
			}
			if w.count >= 1_000_000 {
				return fmt.Errorf("scan item limit 1000000 reached")
			}
			child := path.Join(remote, entry.Name)
			switch {
			case !SafeName(entry.Name):
				w.skip(child, "unsafe name")
			case entry.IsSymlink():
				w.skip(child, "symlink")
			default:
				if err := w.visit(child, parent, entry); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if !info.IsRegular() {
		w.skip(remote, "not a regular file")
		return nil
	}
	relative := strings.TrimPrefix(strings.TrimPrefix(remote, parent), "/")
	local, reason := localPath(w.downloadDir, strings.Split(relative, "/"))
	if reason != "" {
		w.skip(remote, reason)
		return nil
	}
	key := foldingKey(local)
	if first, ok := w.seen[key]; ok {
		w.skip(remote, "duplicate of "+first)
		return nil
	}
	if first, ok := w.dirs[key]; ok {
		w.skip(remote, "duplicate of "+first)
		return nil
	}
	for dir := filepath.Dir(local); dir != w.downloadDir; dir = filepath.Dir(dir) {
		if first, ok := w.seen[foldingKey(dir)]; ok {
			w.skip(remote, "duplicate of "+first)
			return nil
		}
	}
	w.seen[key] = local
	for dir := filepath.Dir(local); dir != w.downloadDir; dir = filepath.Dir(dir) {
		key := foldingKey(dir)
		if _, ok := w.dirs[key]; !ok {
			w.dirs[key] = local
		}
	}
	w.count++
	item := Item{remote, local, info.Size, info.ModTime}
	if w.emit != nil {
		w.emit(item)
	} else {
		w.items = append(w.items, item)
	}
	return nil
}
