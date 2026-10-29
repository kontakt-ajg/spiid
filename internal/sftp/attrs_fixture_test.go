package sftp

type extPair struct {
	Type, Data string
}

// attrs is the wire form of ATTRS.
type fixtureAttrs struct {
	flags      uint32
	size       uint64
	uid, gid   uint32
	perm       uint32
	atime      uint32
	mtime      uint32
	extensions []extPair
}

// appendTo encodes a in wire order.
func (a fixtureAttrs) appendTo(b []byte) []byte {
	b = appendU32(b, a.flags)
	if a.flags&attrSize != 0 {
		b = appendU64(b, a.size)
	}
	if a.flags&attrUIDGID != 0 {
		b = appendU32(b, a.uid)
		b = appendU32(b, a.gid)
	}
	if a.flags&attrPermissions != 0 {
		b = appendU32(b, a.perm)
	}
	if a.flags&attrACModTime != 0 {
		b = appendU32(b, a.atime)
		b = appendU32(b, a.mtime)
	}
	if a.flags&attrExtended != 0 {
		b = appendU32(b, uint32(len(a.extensions)))
		for _, e := range a.extensions {
			b = appendString(b, e.Type)
			b = appendString(b, e.Data)
		}
	}
	return b
}
