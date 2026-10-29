package app

import "time"

const maxMessages = 500

// Message is something the user should see that is not a job: a skipped item, a walk error, a warning.
type Message struct {
	// Time is when the message was recorded.
	Time time.Time
	// Text is the user-facing message.
	Text string
}

func (s *Service) addMessage(text string) {
	s.recordMessage(text, false)
}

func (s *Service) recordMessage(text string, failed bool) {
	if failed {
		s.log.Error("%s", text)
	} else {
		s.log.Info("%s", text)
	}
	s.msgMu.Lock()
	defer s.msgMu.Unlock()
	s.msgs = append(s.msgs, Message{Time: time.Now(), Text: text})
	if n := len(s.msgs) - maxMessages; n > 0 {
		s.msgs = append([]Message(nil), s.msgs[n:]...)
		s.msgBase += n
	}
}

// Messages returns the messages with index >= since and the next index. Indexes count up forever.
func (s *Service) Messages(since int) ([]Message, int) {
	s.msgMu.Lock()
	defer s.msgMu.Unlock()
	next := s.msgBase + len(s.msgs)
	since = max(since, s.msgBase)
	if since >= next {
		return nil, next
	}
	return append([]Message(nil), s.msgs[since-s.msgBase:]...), next
}
