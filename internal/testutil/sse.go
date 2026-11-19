package testutil

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// SSE is an event stream (GET /events) read like the app reads it.
type SSE struct {
	Resp  *http.Response
	lines chan sseLine
}

type sseLine struct {
	text string
	err  error
}

// SSEEvent is an event of the stream, or a comment (Comment set, the
// other fields empty).
type SSEEvent struct {
	ID      int
	Type    string
	Data    string
	Comment string
}

// OpenSSE sends a signed GET /api/v1/events from c. The caller checks the
// status; the body is closed at the end of the test.
func OpenSSE(t testing.TB, c *Client) *SSE {
	t.Helper()
	req := c.Request(t, http.MethodGet, "/api/v1/events", nil)
	httpc := c.HTTP
	if httpc == nil {
		httpc = http.DefaultClient
	}
	resp, err := httpc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	s := &SSE{Resp: resp, lines: make(chan sseLine, 1024)}
	go func() {
		br := bufio.NewReader(resp.Body)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				s.lines <- sseLine{err: err}
				close(s.lines)
				return
			}
			s.lines <- sseLine{text: strings.TrimSuffix(line, "\n")}
		}
	}()
	return s
}

// ErrSSETimeout is returned by Next when nothing came in time.
var ErrSSETimeout = errors.New("sse: timeout")

// Next returns the next event or comment, io.EOF when the stream ended.
func (s *SSE) Next(timeout time.Duration) (SSEEvent, error) {
	deadline := time.After(timeout)
	var ev SSEEvent
	started := false
	for {
		select {
		case l, ok := <-s.lines:
			if !ok {
				return SSEEvent{}, io.EOF
			}
			if l.err != nil {
				if started {
					return SSEEvent{}, io.ErrUnexpectedEOF
				}
				return SSEEvent{}, l.err
			}
			switch {
			case l.text == "":
				if started {
					return ev, nil
				}
			case strings.HasPrefix(l.text, ":"):
				if !started {
					return SSEEvent{Comment: strings.TrimSpace(l.text[1:])}, nil
				}
			default:
				started = true
				field, value, _ := strings.Cut(l.text, ":")
				value = strings.TrimPrefix(value, " ")
				switch field {
				case "id":
					ev.ID, _ = strconv.Atoi(value)
				case "event":
					ev.Type = value
				case "data":
					ev.Data += value
				}
			}
		case <-deadline:
			return SSEEvent{}, ErrSSETimeout
		}
	}
}

// NextEvent returns the next event, skipping the comments.
func (s *SSE) NextEvent(timeout time.Duration) (SSEEvent, error) {
	deadline := time.Now().Add(timeout)
	for {
		ev, err := s.Next(time.Until(deadline))
		if err != nil || ev.Comment == "" {
			return ev, err
		}
	}
}
