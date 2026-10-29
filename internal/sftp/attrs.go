package sftp

import (
	"errors"
	"io/fs"
	"math"
	"path"
	"time"
)

// ATTRS flags.
const (
	attrSize        = 0x1
	attrUIDGID      = 0x2
	attrPermissions = 0x4
	attrACModTime   = 0x8
	attrExtended    = 0x80000000
)

// attrs is the wire form of ATTRS.
type attrs struct {
	flags uint32
	size  uint64
	perm  uint32
	mtime uint32
}

// parseAttrs decodes ATTRS from d (check d.err afterwards).
func parseAttrs(d *decoder) attrs {
	var a attrs
	a.flags = d.u32()
	if a.flags&attrSize != 0 {
		a.size = d.u64()
	}
	if a.flags&attrUIDGID != 0 {
		d.u32()
		d.u32()
	}
	if a.flags&attrPermissions != 0 {
		a.perm = d.u32()
	}
	if a.flags&attrACModTime != 0 {
		d.u32()
		a.mtime = d.u32()
	}
	if a.flags&attrExtended != 0 {
		n := d.u32()
		for i := uint32(0); i < n && d.err == nil; i++ {
			d.str()
			d.str()
		}
	}
	return a
}

// FileInfo describes a remote file.
type FileInfo struct {
	Name    string // base name
	Size    int64
	Mode    fs.FileMode // from SFTP permissions: type bits (dir, symlink, regular) + perm bits
	ModTime time.Time   // seconds resolution
}

// IsDir reports whether the entry is a folder.
func (f FileInfo) IsDir() bool {
	return f.Mode.IsDir()
}

// IsRegular reports whether the entry is a regular file.
func (f FileInfo) IsRegular() bool {
	return f.Mode.IsRegular()
}

// IsSymlink reports whether the entry is a symbolic link.
func (f FileInfo) IsSymlink() bool {
	return f.Mode&fs.ModeSymlink != 0
}

// modeFromPerm converts POSIX permission and type bits.
func modeFromPerm(p uint32) fs.FileMode {
	m := fs.FileMode(p & 0o777)
	switch p & 0o170000 {
	case 0o040000:
		m |= fs.ModeDir
	case 0o100000:
	case 0o120000:
		m |= fs.ModeSymlink
	case 0o010000:
		m |= fs.ModeNamedPipe
	case 0o140000:
		m |= fs.ModeSocket
	case 0o020000:
		m |= fs.ModeDevice | fs.ModeCharDevice
	case 0o060000:
		m |= fs.ModeDevice
	default:
		m |= fs.ModeIrregular
	}
	return m
}

var errBadSize = errors.New("file size out of range")

// fileInfo converts a to a FileInfo named name.
func (a attrs) fileInfo(name string) (FileInfo, error) {
	fi := FileInfo{Name: name, Mode: fs.ModeIrregular}
	if a.flags&attrSize != 0 {
		if a.size > math.MaxInt64 {
			return fi, errBadSize
		}
		fi.Size = int64(a.size)
	}
	if a.flags&attrPermissions != 0 {
		fi.Mode = modeFromPerm(a.perm)
	}
	if a.flags&attrACModTime != 0 {
		fi.ModTime = time.Unix(int64(a.mtime), 0)
	}
	return fi, nil
}

func baseName(p string) string {
	return path.Base(p)
}
