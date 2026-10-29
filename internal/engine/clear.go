package engine

import "strings"

// ClearFinished removes successful and canceled jobs, including their deduplication entries.
func (m *Manager) ClearFinished() {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.jobs[:0]
	for _, j := range m.jobs {
		j.mu.Lock()
		remove := j.pub.State == Done || j.pub.State == Skipped || j.pub.State == Canceled
		j.mu.Unlock()
		if remove {
			delete(m.byKey, strings.ToLower(strings.ToUpper(j.req.Local)))
		} else {
			kept = append(kept, j)
		}
	}
	clear(m.jobs[len(kept):])
	m.jobs = kept
}
