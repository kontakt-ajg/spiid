package app

import (
	"sync"
	"testing"

	"spiid/internal/config"
)

func TestSaveSettingsNormalizesAndPersists(t *testing.T) {
	e := newEnv(t, sftpOpts())
	s := e.service()
	if err := s.SaveSettings(
		config.Settings{DownloadDir: e.dl, ThreadsPerFile: 999, ParallelFiles: -1, Theme: "bogus"}); err != nil {
		t.Fatal(err)
	}
	st := s.Settings()
	if st.ThreadsPerFile != 32 || st.ParallelFiles < 1 || st.MaxConnections < 1 || st.Theme != config.ThemeSystem {
		t.Fatalf("not normalized: %+v", st)
	}
	disk, err := config.NewStore(e.cfg, e.home).LoadSettings()
	if err != nil || disk != st {
		t.Fatalf("on disk %+v (%v), in memory %+v", disk, err, st)
	}
	s2 := e.service()
	if s2.Settings() != st {
		t.Fatalf("reloaded %+v", s2.Settings())
	}
}

func TestConcurrentSettingsMatchDisk(t *testing.T) {
	e := newEnv(t, sftpOpts())
	s := e.service()
	for round := 0; round < 100; round++ {
		var wg sync.WaitGroup
		for i := 1; i <= 12; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				st := s.Settings()
				st.ThreadsPerFile = i
				if err := s.SaveSettings(st); err != nil {
					t.Error(err)
				}
			}(i)
		}
		wg.Wait()
		disk, err := config.NewStore(e.cfg, e.home).LoadSettings()
		if err != nil {
			t.Fatal(err)
		}
		if disk != s.Settings() {
			t.Fatalf("round %d: disk threads=%d, memory threads=%d", round, disk.ThreadsPerFile, s.Settings().ThreadsPerFile)
		}
	}
}
