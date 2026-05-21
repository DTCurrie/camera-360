package camera360

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"go.viam.com/rdk/logging"
)

const (
	defaultAmbarellaPort = 7878

	msgStartSession = 257
	msgEndSession   = 258
	msgStartPreview = 259
	msgStopPreview  = 260
	msgGetSettings  = 3

	rvalSessionInvalid = -4
)

type ambaRequest struct {
	MsgID int    `json:"msg_id"`
	Token int    `json:"token"`
	Type  string `json:"type,omitempty"`
	Param any    `json:"param,omitempty"`
}

type ambaResponse struct {
	Rval  int             `json:"rval"`
	MsgID int             `json:"msg_id"`
	Type  string          `json:"type,omitempty"`
	Param json.RawMessage `json:"param,omitempty"`
}

// Session holds an authenticated Ambarella control connection. Methods are safe
// for concurrent use. The handshake unlocks the camera's RTSP preview at
// rtsp://<host>:554/live; the session must stay open while the stream is being
// consumed.
type Session struct {
	host   string
	port   int
	conn   net.Conn
	token  int
	logger logging.Logger

	mu     sync.Mutex
	closed bool
}

// DialSession opens a TCP connection to the camera's Ambarella control port and
// performs the start_session + start_preview handshake. On success, the camera
// is serving RTSP at rtsp://<host>:554/live.
func DialSession(ctx context.Context, host string, port int, logger logging.Logger) (*Session, error) {
	if port == 0 {
		port = defaultAmbarellaPort
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial ambarella control %s: %w", addr, err)
	}
	s := &Session{host: host, port: port, conn: conn, logger: logger}

	if err := s.handshake(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return s, nil
}

func (s *Session) handshake() error {
	resp, err := s.exchange(ambaRequest{MsgID: msgStartSession, Token: 0})
	if err != nil {
		return fmt.Errorf("start_session: %w", err)
	}
	if resp.Rval != 0 {
		return fmt.Errorf("start_session rejected: rval=%d", resp.Rval)
	}
	var token int
	if err := json.Unmarshal(resp.Param, &token); err != nil {
		return fmt.Errorf("start_session param: %w", err)
	}
	s.token = token
	s.logger.Debugw("ambarella session established", "token", token)

	resp, err = s.exchange(ambaRequest{MsgID: msgStartPreview, Token: token, Param: "none_force"})
	if err != nil {
		return fmt.Errorf("start_preview: %w", err)
	}
	if resp.Rval != 0 {
		return fmt.Errorf("start_preview rejected: rval=%d", resp.Rval)
	}
	return nil
}

// exchange writes one request and reads one response. The Ambarella firmware
// emits JSON objects on the socket with no delimiter between them, so we
// brace-count to find object boundaries.
func (s *Session) exchange(req ambaRequest) (*ambaResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("session closed")
	}
	if err := s.writeJSON(req); err != nil {
		return nil, err
	}
	return s.readOneJSON()
}

func (s *Session) writeJSON(req ambaRequest) error {
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_ = s.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, err := s.conn.Write(b); err != nil {
		return fmt.Errorf("write %d bytes: %w", len(b), err)
	}
	return nil
}

func (s *Session) readOneJSON() (*ambaResponse, error) {
	_ = s.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var buf []byte
	chunk := make([]byte, 1024)
	depth := 0
	started := false
	inStr := false
	esc := false
	for {
		n, err := s.conn.Read(chunk)
		if n > 0 {
			for i := 0; i < n; i++ {
				c := chunk[i]
				buf = append(buf, c)
				if inStr {
					if esc {
						esc = false
					} else if c == '\\' {
						esc = true
					} else if c == '"' {
						inStr = false
					}
					continue
				}
				switch c {
				case '"':
					inStr = true
				case '{':
					depth++
					started = true
				case '}':
					depth--
					if started && depth == 0 {
						var resp ambaResponse
						if jerr := json.Unmarshal(buf, &resp); jerr != nil {
							return nil, fmt.Errorf("parse json %q: %w", string(buf), jerr)
						}
						return &resp, nil
					}
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil, fmt.Errorf("connection closed after %d bytes (partial=%q)", len(buf), string(buf))
			}
			return nil, err
		}
	}
}

// RTSPURL returns the live preview URL unlocked by this session.
func (s *Session) RTSPURL() string {
	return fmt.Sprintf("rtsp://%s:554/live", s.host)
}

// Heartbeat sends a cheap get_settings probe to keep the session alive. Returns
// an error if the camera no longer recognizes our token.
func (s *Session) Heartbeat() error {
	resp, err := s.exchange(ambaRequest{MsgID: msgGetSettings, Token: s.token})
	if err != nil {
		return err
	}
	if resp.Rval == rvalSessionInvalid {
		return errors.New("session token invalid; camera dropped session")
	}
	return nil
}

// Close tears down the preview stream and ends the session.
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	conn := s.conn
	token := s.token
	s.mu.Unlock()

	if token != 0 {
		_, _ = s.exchangeBypass(conn, ambaRequest{MsgID: msgStopPreview, Token: token})
		_, _ = s.exchangeBypass(conn, ambaRequest{MsgID: msgEndSession, Token: token})
	}
	return conn.Close()
}

// exchangeBypass is used in Close() after the mutex+closed flag have already
// been claimed; it talks to conn directly without re-locking.
func (s *Session) exchangeBypass(conn net.Conn, req ambaRequest) (*ambaResponse, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(b); err != nil {
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	chunk := make([]byte, 512)
	var buf []byte
	depth, started, inStr, esc := 0, false, false, false
	for {
		n, rerr := conn.Read(chunk)
		if n > 0 {
			for i := 0; i < n; i++ {
				c := chunk[i]
				buf = append(buf, c)
				if inStr {
					if esc {
						esc = false
					} else if c == '\\' {
						esc = true
					} else if c == '"' {
						inStr = false
					}
					continue
				}
				switch c {
				case '"':
					inStr = true
				case '{':
					depth++
					started = true
				case '}':
					depth--
					if started && depth == 0 {
						var resp ambaResponse
						if jerr := json.Unmarshal(buf, &resp); jerr != nil {
							return nil, jerr
						}
						return &resp, nil
					}
				}
			}
		}
		if rerr != nil {
			return nil, rerr
		}
	}
}
