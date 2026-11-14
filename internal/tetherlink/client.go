// Package tetherlink calls tools on a Tether MCP gateway. Tether fronts the other apps
// (Tesseract, Torque, Fragments Engine and the rest), so one connection reaches the records
// Basalisk previews and opens, with whatever credential the configured command carries and
// nothing stored here.
//
// The gateway is a child process speaking MCP over stdio: the same command an agent session
// is launched with (typically ssh to the host that runs Tether). It is started on first use
// and restarted when it dies.
package tetherlink

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ErrNotConfigured means no gateway command was given.
var ErrNotConfigured = errors.New("no Tether gateway is configured")

// ToolError is an error the tool itself reported (as opposed to the connection failing).
type ToolError struct{ Message string }

func (e *ToolError) Error() string { return e.Message }

// Client is one gateway connection, safe for concurrent use.
type Client struct {
	argv []string

	mu        sync.Mutex // guards the fields below
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	pending   map[int64]chan rpcMessage
	nextID    int64
	ready     bool
	lastStart time.Time
	writeMu   sync.Mutex
	startMu   sync.Mutex
}

type rpcMessage struct {
	ID     *int64          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// New returns a client for the gateway started by argv. A nil or empty argv gives a client
// whose calls answer ErrNotConfigured.
func New(argv []string) *Client { return &Client{argv: argv, pending: map[int64]chan rpcMessage{}} }

// Configured reports whether a gateway command was given.
func (c *Client) Configured() bool { return c != nil && len(c.argv) > 0 }

// Close stops the gateway process.
func (c *Client) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopLocked(errors.New("closed"))
}

func (c *Client) stopLocked(reason error) {
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	c.cmd, c.stdin, c.ready = nil, nil, false
	for id, ch := range c.pending {
		ch <- rpcMessage{Error: &struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{Code: -1, Message: reason.Error()}}
		delete(c.pending, id)
	}
}

// start launches the gateway and performs the MCP handshake. Starts are serialized by
// startMu, so concurrent first calls share one process.
func (c *Client) start(ctx context.Context) error {
	c.mu.Lock()
	if c.ready {
		c.mu.Unlock()
		return nil
	}
	if since := time.Since(c.lastStart); since < 2*time.Second && !c.lastStart.IsZero() {
		c.mu.Unlock()
		return fmt.Errorf("the Tether gateway failed to start %s ago; retrying shortly", since.Round(time.Millisecond))
	}
	c.lastStart = time.Now()
	cmd := exec.Command(c.argv[0], c.argv[1:]...)
	stdin, err := cmd.StdinPipe()
	if err == nil {
		var stdout, stderr io.ReadCloser
		if stdout, err = cmd.StdoutPipe(); err == nil {
			stderr, _ = cmd.StderrPipe()
			if err = cmd.Start(); err == nil {
				c.cmd, c.stdin = cmd, stdin
				go c.read(cmd, stdout)
				go func() {
					sc := bufio.NewScanner(stderr)
					sc.Buffer(make([]byte, 64<<10), 1<<20)
					for sc.Scan() {
						slog.Debug("tether gateway", "stderr", sc.Text())
					}
				}()
			}
		}
	}
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("start the Tether gateway: %w", err)
	}

	fail := func(err error) error {
		c.mu.Lock()
		c.stopLocked(err)
		c.mu.Unlock()
		return fmt.Errorf("the Tether gateway did not initialize: %w", err)
	}
	if _, err := c.roundTrip(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "basalisk", "version": "0"},
	}); err != nil {
		return fail(err)
	}
	if err := c.write(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		return fail(err)
	}
	c.mu.Lock()
	c.ready = c.cmd == cmd
	c.mu.Unlock()
	return nil
}

func (c *Client) read(cmd *exec.Cmd, stdout io.Reader) {
	r := bufio.NewReaderSize(stdout, 256<<10)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var msg rpcMessage
			if json.Unmarshal(line, &msg) == nil && msg.ID != nil && msg.Method == "" {
				c.mu.Lock()
				ch := c.pending[*msg.ID]
				delete(c.pending, *msg.ID)
				c.mu.Unlock()
				if ch != nil {
					ch <- msg
				}
			}
		}
		if err != nil {
			break
		}
	}
	_ = cmd.Wait()
	c.mu.Lock()
	if c.cmd == cmd {
		c.stopLocked(errors.New("the Tether gateway exited"))
	}
	c.mu.Unlock()
}

func (c *Client) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	stdin := c.stdin
	c.mu.Unlock()
	if stdin == nil {
		return errors.New("the Tether gateway is not running")
	}
	_, err = stdin.Write(append(data, '\n'))
	return err
}

func (c *Client) roundTrip(ctx context.Context, method string, params any) (json.RawMessage, error) {
	ch := make(chan rpcMessage, 1)
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case msg := <-ch:
		if msg.Error != nil {
			return nil, errors.New(msg.Error.Message)
		}
		return msg.Result, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Call runs one tool and returns its text result (JSON, for the tools Basalisk uses). A
// *ToolError means the tool answered with an error; anything else is the connection. A
// large result comes back cut, with the gateway's note about it after the JSON; CallAll
// reads the whole of one.
func (c *Client) Call(ctx context.Context, tool string, args map[string]any) ([]byte, error) {
	parts, err := c.call(ctx, tool, args)
	return []byte(strings.Join(parts, "")), err
}

// call runs one tool and returns the text parts of its result.
func (c *Client) call(ctx context.Context, tool string, args map[string]any) ([]string, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	c.startMu.Lock()
	err := c.start(ctx)
	c.startMu.Unlock()
	if err != nil {
		return nil, err
	}

	raw, err := c.roundTrip(ctx, "tools/call", map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return nil, err
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("unreadable tool result: %w", err)
	}
	var parts []string
	for _, part := range result.Content {
		if part.Type == "text" {
			parts = append(parts, part.Text)
		}
	}
	if result.IsError {
		return nil, &ToolError{Message: strings.Join(parts, "")}
	}
	return parts, nil
}

// maxPages bounds how far CallAll follows a cut result.
const maxPages = 60

// CallAll runs one tool and returns its whole JSON result. The gateway cuts a large result
// at one of its lists and says so in a second part ({"gateway_truncated": {"path",
// "next_cursor"}}); the rest is fetched with tether_result_next and the list put back
// together, so the caller sees one object as if nothing had been cut.
func (c *Client) CallAll(ctx context.Context, tool string, args map[string]any) ([]byte, error) {
	parts, err := c.call(ctx, tool, args)
	if err != nil || len(parts) < 2 {
		return []byte(strings.Join(parts, "")), err
	}
	type cut struct {
		G *struct {
			Path string `json:"path"`
			Next string `json:"next_cursor"`
		} `json:"gateway_truncated"`
	}
	note := func(parts []string) (path, next string, ok bool) {
		var n cut
		if len(parts) < 2 || json.Unmarshal([]byte(parts[len(parts)-1]), &n) != nil || n.G == nil {
			return "", "", false
		}
		return n.G.Path, n.G.Next, true
	}
	path, next, ok := note(parts)
	if !ok {
		return []byte(strings.Join(parts, "")), nil
	}
	var whole map[string]json.RawMessage
	if err := json.Unmarshal([]byte(parts[0]), &whole); err != nil {
		return nil, fmt.Errorf("%s: unreadable result: %w", tool, err)
	}
	var list []json.RawMessage
	if err := json.Unmarshal(whole[path], &list); err != nil {
		return nil, fmt.Errorf("%s: the cut list %q is unreadable: %w", tool, path, err)
	}
	for page := 0; next != "" && page < maxPages; page++ {
		more, err := c.call(ctx, "tether_result_next", map[string]any{"cursor": next})
		if err != nil {
			return nil, fmt.Errorf("%s: reading the rest of the result: %w", tool, err)
		}
		if len(more) == 0 {
			break
		}
		var obj map[string]json.RawMessage
		var rest []json.RawMessage
		if json.Unmarshal([]byte(more[0]), &obj) != nil || json.Unmarshal(obj[path], &rest) != nil {
			return nil, fmt.Errorf("%s: the rest of the result is unreadable", tool)
		}
		list = append(list, rest...)
		if _, next, ok = note(more); !ok {
			next = ""
		}
	}
	whole[path], _ = json.Marshal(list)
	return json.Marshal(whole)
}

// ToolInfo is one tool the gateway offers, as tools/list describes it.
type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Tools lists the gateway's tools (every page of tools/list).
func (c *Client) Tools(ctx context.Context) ([]ToolInfo, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	c.startMu.Lock()
	err := c.start(ctx)
	c.startMu.Unlock()
	if err != nil {
		return nil, err
	}
	var out []ToolInfo
	cursor := ""
	for range 50 { // a bound, not an expectation
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.roundTrip(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var page struct {
			Tools      []ToolInfo `json:"tools"`
			NextCursor string     `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("unreadable tool list: %w", err)
		}
		out = append(out, page.Tools...)
		if page.NextCursor == "" {
			return out, nil
		}
		cursor = page.NextCursor
	}
	return out, nil
}
