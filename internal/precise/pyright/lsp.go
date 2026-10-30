package pyright

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// conn is a JSON-RPC connection to a language server over its stdio: the
// requests it sends us are answered by answer, its notifications dropped.
type conn struct {
	w      io.Writer
	wmu    sync.Mutex
	mu     sync.Mutex
	next   int
	wait   map[int]chan rpcMsg
	answer func(method string, params json.RawMessage) any
	err    error // set when the read loop ends
	done   chan struct{}
}

type rpcMsg struct {
	ID     *json.RawMessage `json:"id,omitempty"`
	Method string           `json:"method,omitempty"`
	Params json.RawMessage  `json:"params,omitempty"`
	Result json.RawMessage  `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func newConn(r io.Reader, w io.Writer, answer func(string, json.RawMessage) any) *conn {
	c := &conn{w: w, wait: map[int]chan rpcMsg{}, answer: answer, done: make(chan struct{})}
	go c.read(bufio.NewReaderSize(r, 1<<20))
	return c
}

func (c *conn) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(b)); err != nil {
		return err
	}
	_, err = c.w.Write(b)
	return err
}

func (c *conn) read(r *bufio.Reader) {
	var err error
	defer func() {
		c.mu.Lock()
		c.err = err
		for id, ch := range c.wait {
			close(ch)
			delete(c.wait, id)
		}
		c.mu.Unlock()
		close(c.done)
	}()
	for {
		n := -1
		for {
			var ln string
			if ln, err = r.ReadString('\n'); err != nil {
				return
			}
			ln = strings.TrimRight(ln, "\r\n")
			if ln == "" {
				break
			}
			if v, ok := strings.CutPrefix(strings.ToLower(ln), "content-length:"); ok {
				n, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		if n < 0 {
			err = errors.New("language server: a message without Content-Length")
			return
		}
		b := make([]byte, n)
		if _, err = io.ReadFull(r, b); err != nil {
			return
		}
		var m rpcMsg
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		switch {
		case m.Method != "" && m.ID != nil:
			// a request of the server's: answered off the read loop
			id := *m.ID
			go func() {
				_ = c.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": c.answer(m.Method, m.Params)})
			}()
		case m.Method == "" && m.ID != nil:
			id, err := strconv.Atoi(string(*m.ID))
			if err != nil {
				continue
			}
			c.mu.Lock()
			ch := c.wait[id]
			delete(c.wait, id)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		}
	}
}

// call sends a request and waits for its answer.
func (c *conn) call(ctx context.Context, method string, params any, result any) error {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.err
	}
	c.next++
	id := c.next
	ch := make(chan rpcMsg, 1)
	c.wait[id] = ch
	c.mu.Unlock()
	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			if c.err != nil {
				return fmt.Errorf("language server stopped: %w", c.err)
			}
			return errors.New("language server stopped")
		}
		if m.Error != nil {
			return fmt.Errorf("%s: %s", method, m.Error.Message)
		}
		if result != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, result)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.wait, id)
		c.mu.Unlock()
		_ = c.write(map[string]any{"jsonrpc": "2.0", "method": "$/cancelRequest", "params": map[string]any{"id": id}})
		return ctx.Err()
	}
}

func (c *conn) notify(method string, params any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// server is a running language server.
type server struct {
	cmd  *exec.Cmd
	conn *conn
	in   io.WriteCloser
}

// startServer runs argv (the langserver with --stdio) and initializes it on
// root with the analysis settings given.
func startServer(ctx context.Context, argv []string, root string, settings map[string]any) (*server, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stderr = io.Discard
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	answer := func(method string, params json.RawMessage) any {
		if method != "workspace/configuration" {
			return nil // registerCapability, workDoneProgress/create …
		}
		var p struct {
			Items []struct {
				Section string `json:"section"`
			} `json:"items"`
		}
		_ = json.Unmarshal(params, &p)
		res := make([]any, len(p.Items))
		for i, it := range p.Items {
			res[i] = settings[it.Section]
		}
		return res
	}
	s := &server{cmd: cmd, in: in, conn: newConn(out, in, answer)}
	ictx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	init := map[string]any{
		"processId": nil,
		"rootUri":   fileURI(root),
		"capabilities": map[string]any{
			"workspace": map[string]any{"configuration": true, "workspaceFolders": true},
			"textDocument": map[string]any{
				"definition":      map[string]any{"linkSupport": false},
				"synchronization": map[string]any{"didSave": false},
			},
		},
		"workspaceFolders": []any{map[string]any{"uri": fileURI(root), "name": "root"}},
	}
	var res struct {
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := s.conn.call(ictx, "initialize", init, &res); err != nil {
		s.kill()
		return nil, fmt.Errorf("starting the language server: %w", err)
	}
	if err := s.conn.notify("initialized", map[string]any{}); err != nil {
		s.kill()
		return nil, err
	}
	_ = s.conn.notify("workspace/didChangeConfiguration", map[string]any{"settings": settings})
	return s, nil
}

func (s *server) kill() {
	_ = s.cmd.Process.Kill()
	_ = s.cmd.Wait()
}

// close shuts the server down, killing it when it does not exit.
func (s *server) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = s.conn.call(ctx, "shutdown", nil, nil)
	_ = s.conn.notify("exit", nil)
	_ = s.in.Close()
	done := make(chan struct{})
	go func() { _ = s.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = s.cmd.Process.Kill()
		<-done
	}
}

// location is an LSP Location.
type location struct {
	URI   string `json:"uri"`
	Range struct {
		Start struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"start"`
	} `json:"range"`
}

func fileURI(p string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(p)}
	return u.String()
}

func uriPath(u string) string {
	x, err := url.Parse(u)
	if err != nil || x.Scheme != "file" {
		return ""
	}
	return filepath.FromSlash(x.Path)
}
