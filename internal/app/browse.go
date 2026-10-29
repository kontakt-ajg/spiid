package app

import (
	"context"
	"errors"
	"path"
	"sort"
	"strings"
	"time"

	"spiid/internal/errs"
	"spiid/internal/sftp"
)

// Entry is one item of a remote folder.
type Entry struct {
	// Name is the entry name within its folder.
	Name string
	// Path is the absolute remote path.
	Path string
	// IsDir reports whether the entry resolves to a directory.
	IsDir bool
	// IsLink reports whether the entry is a symbolic link.
	IsLink bool
	// Size is the file size in bytes.
	Size int64
	// ModTime is the remote modification time.
	ModTime time.Time
}

// Listing is the content of a remote folder.
type Listing struct {
	// Path is the absolute remote path.
	Path string // absolute, resolved
	// Entries contains folders first, then files, sorted by name.
	Entries []Entry // folders first, then files, each sorted by name (case-insensitive)
}

// List reads a remote folder; dir "" is the server's start folder (or the login folder).
func (s *Service) List(ctx context.Context, server, dir string) (Listing, error) {
	srv, ok := s.findServer(server)
	if !ok {
		return Listing{}, errs.New(errs.InvalidInput, "list", server, errors.New("unknown server"))
	}
	pool := s.ssh.Pool(srv, s.Settings().MaxConnections)
	conn, err := pool.Get(ctx)
	if err != nil {
		return Listing{}, err
	}
	if dir == "" {
		dir = srv.StartDir
	}
	if dir == "" {
		dir = "."
	}
	l, err := listDir(ctx, conn.SFTP, dir)
	if errs.KindOf(err) == errs.ConnectionLost {
		pool.Discard(conn)
	} else {
		pool.Put(conn)
	}
	return l, err
}

func listDir(ctx context.Context, c *sftp.Client, dir string) (Listing, error) {
	abs, err := c.RealPath(ctx, dir)
	if err != nil {
		return Listing{}, err
	}
	infos, err := c.ReadDir(ctx, abs)
	if err != nil {
		return Listing{}, err
	}
	l := Listing{Path: abs, Entries: make([]Entry, 0, len(infos))}
	for _, fi := range infos {
		e := Entry{
			Name:    fi.Name,
			Path:    path.Join(abs, fi.Name),
			IsDir:   fi.IsDir(),
			Size:    fi.Size,
			ModTime: fi.ModTime,
		}
		if fi.IsSymlink() {
			e.IsLink = true
			target, err := c.Stat(ctx, e.Path)
			switch {
			case err == nil:
				e.IsDir, e.Size, e.ModTime = target.IsDir(), target.Size, target.ModTime
			case errs.KindOf(err) == errs.ConnectionLost || ctx.Err() != nil:
				return Listing{}, err
			default:
				e.IsDir = false
			}
		}
		l.Entries = append(l.Entries, e)
	}
	sort.Slice(l.Entries, func(i, j int) bool {
		a, b := l.Entries[i], l.Entries[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		if la, lb := strings.ToLower(a.Name), strings.ToLower(b.Name); la != lb {
			return la < lb
		}
		return a.Name < b.Name
	})
	return l, nil
}
