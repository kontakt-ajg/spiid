package engine

import (
	"os"
	"testing"

	"spiid/internal/errs"
)

func TestOpenPartUsesPrivatePermissions(t *testing.T) {
	p := t.TempDir() + "/part"
	os.WriteFile(p, []byte("old"), 0o644)
	f, err := openPart(p, 20, true)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, _ := f.Stat()
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("partial mode = %o, want 600", st.Mode().Perm())
	}
}

func TestDownloadPreservesLateLocalFile(t *testing.T) {
	e := newEnv(t, nil)
	e.remoteFile("x", 128)
	local := e.req("x").Local
	late := []byte("created by another app during download")
	e.pool.beforeGet = func(n int32) {
		if n != 3 {
			return
		}
		if err := os.WriteFile(local, late, 0o644); err != nil {
			t.Error(err)
		}
	}
	f := e.waitState(e.add("x").ID, Failed)
	if f.ErrKind != errs.LocalExists {
		t.Errorf("job = %+v", f)
	}
	equalFile(t, local, late)
	exists(t, local+partSuffix, local+stateSuffix)
}
