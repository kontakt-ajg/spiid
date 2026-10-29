package web

import (
	"context"

	"spiid/internal/app"
)

func downloadsState(s *Server, _ context.Context, p sinceParams) (any, error) {
	jobs := s.service.Jobs()
	sum := app.Summarize(jobs)
	ms, next := s.service.Messages(*p.Since)
	type message struct {
		Time int64  `json:"time"`
		Text string `json:"text"`
	}
	messages := make([]message, 0, len(ms))
	for _, m := range ms {
		messages = append(messages, message{m.Time.UnixMilli(), m.Text})
	}
	type summary struct {
		Speed  float64 `json:"speed"`
		Active int     `json:"active"`
		Queued int     `json:"queued"`
	}
	return struct {
		Jobs     []jobJSON  `json:"jobs"`
		Summary  summary    `json:"summary"`
		Scans    []app.Scan `json:"scans"`
		Messages []message  `json:"messages"`
		Next     int        `json:"next"`
	}{jobsJSON(jobs), summary{sum.Speed, sum.Active, sum.Queued}, s.service.Scans(), messages, next}, nil
}

func downloadsCancelScan(s *Server, _ context.Context, p idParams) (any, error) {
	return empty{}, s.service.CancelScan(*p.ID)
}

func downloadsClearFinished(s *Server, _ context.Context, _ empty) (any, error) {
	s.service.ClearFinished()
	return empty{}, nil
}
