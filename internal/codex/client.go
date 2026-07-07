// Package codex speaks line-framed JSON-RPC to a `codex app-server`
// subprocess over stdio. Protocol details: docs/codex-app-server-protocol.md.
//
// Message dispatch order matters: a message with both `method` and `id` is a
// server-initiated request (approvals) and must be checked BEFORE looking up
// our own pending requests, because the two id spaces may collide.
package codex

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var ErrClosed = errors.New("codex app-server exited")

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type inMsg struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type outMsg struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params any             `json:"params,omitempty"`
	Result any             `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type pendingResult struct {
	result json.RawMessage
	err    error
}

// Pid returns the codex subprocess PID (0 if not started). Used by the hub to
// tell its own codex processes apart from foreign holders of a thread.
func (c *Client) Pid() int {
	if c.cmd != nil && c.cmd.Process != nil {
		return c.cmd.Process.Pid
	}
	return 0
}

type Client struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	writeMu sync.Mutex // serializes stdin writes
	mu      sync.Mutex // guards pending, nextID, closed
	pending map[int64]chan pendingResult
	nextID  int64
	closed  bool

	stderrMu   sync.Mutex
	stderrTail string

	OnNotification  func(method string, params json.RawMessage)
	OnServerRequest func(id json.RawMessage, method string, params json.RawMessage)
	OnClose         func()
}

// Spawn starts `codex app-server`. Callbacks must be set before Start.
func Spawn() (*Client, error) {
	cmd := exec.Command("/Users/cp/.nvm/versions/node/v25.2.1/bin/codex", "app-server")
	env := os.Environ()
	filtered := env[:0]
	for _, kv := range env {
		// These trigger managed network restrictions inside codex.
		if strings.HasPrefix(kv, "CODEX_SANDBOX=") ||
			strings.HasPrefix(kv, "CODEX_SANDBOX_NETWORK_DISABLED=") ||
			strings.HasPrefix(kv, "CODEX_CI=") {
			continue
		}
		filtered = append(filtered, kv)
	}
	cmd.Env = filtered

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	c := &Client{cmd: cmd, stdin: stdin, pending: make(map[int64]chan pendingResult)}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn codex app-server: %w", err)
	}
	go c.readStderr(stderr)
	go c.readLoop(stdout)
	return c, nil
}

func (c *Client) readStderr(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			c.stderrMu.Lock()
			c.stderrTail += string(buf[:n])
			if len(c.stderrTail) > 8000 {
				c.stderrTail = c.stderrTail[len(c.stderrTail)-8000:]
			}
			c.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (c *Client) StderrTail() string {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	return c.stderrTail
}

func (c *Client) readLoop(stdout io.Reader) {
	reader := bufio.NewReaderSize(stdout, 1<<20)
	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			c.dispatch(line)
		}
		if err != nil {
			break
		}
	}
	// Process stdout closed: mark closed, fail pending, notify (hub side is
	// idempotent about repeated close notifications).
	c.mu.Lock()
	c.closed = true
	for id, ch := range c.pending {
		delete(c.pending, id)
		ch <- pendingResult{err: ErrClosed}
	}
	c.mu.Unlock()
	go c.cmd.Wait() // reap
	if c.OnClose != nil {
		c.OnClose()
	}
}

func (c *Client) dispatch(line string) {
	var msg inMsg
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return
	}
	hasID := len(msg.ID) > 0 && string(msg.ID) != "null"
	switch {
	case msg.Method != "" && hasID:
		if c.OnServerRequest != nil {
			c.OnServerRequest(msg.ID, msg.Method, msg.Params)
		}
	case hasID:
		var id int64
		if err := json.Unmarshal(msg.ID, &id); err != nil {
			return
		}
		c.mu.Lock()
		ch, ok := c.pending[id]
		if ok {
			delete(c.pending, id)
		}
		c.mu.Unlock()
		if ok {
			if msg.Error != nil {
				ch <- pendingResult{err: fmt.Errorf("%s", msg.Error.Message)}
			} else {
				ch <- pendingResult{result: msg.Result}
			}
		}
	case msg.Method != "":
		if c.OnNotification != nil {
			c.OnNotification(msg.Method, msg.Params)
		}
	}
}

func (c *Client) write(msg outMsg) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(append(data, '\n'))
	return err
}

func (c *Client) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// Request sends a JSON-RPC request and waits for its response.
// NEVER call while holding a lock that the notification callbacks also take.
func (c *Client) Request(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	c.nextID++
	id := c.nextID
	ch := make(chan pendingResult, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	idRaw, _ := json.Marshal(id)
	if params == nil {
		params = map[string]any{}
	}
	if err := c.write(outMsg{ID: idRaw, Method: method, Params: params}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case r := <-ch:
		return r.result, r.err
	case <-time.After(timeout):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("timeout waiting for %s", method)
	}
}

func (c *Client) Notify(method string, params any) error {
	if params == nil {
		params = map[string]any{}
	}
	return c.write(outMsg{Method: method, Params: params})
}

// Respond answers a server-initiated request (e.g. an approval).
func (c *Client) Respond(id json.RawMessage, result any) error {
	return c.write(outMsg{ID: id, Result: result})
}

func (c *Client) RespondError(id json.RawMessage, code int, message string) error {
	return c.write(outMsg{ID: id, Error: &rpcError{Code: code, Message: message}})
}

func (c *Client) Initialize() error {
	_, err := c.Request("initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "codex-hub", "title": "Codex Hub", "version": "0.1.0"},
		"capabilities": map[string]any{"experimentalApi": true},
	}, 20*time.Second)
	if err != nil {
		return fmt.Errorf("initialize: %w (stderr: %s)", err, lastLine(c.StderrTail()))
	}
	_ = c.Notify("initialized", nil)
	time.Sleep(100 * time.Millisecond)
	return nil
}

// Close shuts the subprocess down: stdin EOF + SIGTERM now, SIGKILL after 2s.
func (c *Client) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	for id, ch := range c.pending {
		delete(c.pending, id)
		ch <- pendingResult{err: ErrClosed}
	}
	c.mu.Unlock()

	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Signal(os.Interrupt)
		go func(p *os.Process) {
			time.Sleep(2 * time.Second)
			_ = p.Kill()
		}(c.cmd.Process)
	}
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}
