package app

import "context"

// Scan describes a running selection scan.
type Scan struct {
	ID     int64  `json:"id"`
	Server string `json:"server"`
	Folder string `json:"folder"`
	Files  int    `json:"files"`
}

type runningScan struct {
	Scan
	cancel context.CancelFunc
}

// Scans returns running scans in creation order.
func (s *Service) Scans() []Scan {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Scan, 0, len(s.scans))
	for _, scan := range s.scans {
		out = append(out, scan.Scan)
	}
	return out
}

// CancelScan cancels discovery, preserving queued files.
func (s *Service) CancelScan(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, scan := range s.scans {
		if scan.ID == id {
			scan.cancel()
			return nil
		}
	}
	return nil // the scan already ended
}

func (s *Service) nextScanID() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextScan++
	return s.nextScan
}
