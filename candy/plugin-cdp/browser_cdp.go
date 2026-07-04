package cdp

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/websocket"
)

// browser_cdp.go is the plugin's own lightweight Chrome DevTools Protocol WebSocket
// client (moved from charly/browser_cdp.go). Unlike the host copy it carries NO
// CheckEndpoint — the host pre-resolves the deployment's CDP port to a host-reachable
// DevTools base URL and owns any ssh -L forward's lifetime, so the plugin dials the
// already-host-routable URL and only closes the WebSocket on Close.

// cdpMessage represents a Chrome DevTools Protocol message (request or response).
type cdpMessage struct {
	ID     int             `json:"id"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *cdpError       `json:"error,omitempty"`
}

// cdpError represents an error returned by a CDP method call.
type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *cdpError) Error() string {
	return fmt.Sprintf("CDP error %d: %s", e.Code, e.Message)
}

// CDPClient is a lightweight Chrome DevTools Protocol WebSocket client.
type CDPClient struct {
	ws      *websocket.Conn
	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[int]chan cdpMessage
	done    chan struct{}
}

// NewCDPClient connects to a CDP WebSocket endpoint and starts reading messages.
func NewCDPClient(wsURL string) (*CDPClient, error) {
	ws, err := websocket.Dial(wsURL, "", "http://localhost")
	if err != nil {
		return nil, fmt.Errorf("connecting to CDP WebSocket %s: %w", wsURL, err)
	}

	c := &CDPClient{
		ws:      ws,
		pending: make(map[int]chan cdpMessage),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

// readLoop reads messages from the WebSocket and dispatches responses to pending callers.
func (c *CDPClient) readLoop() {
	defer close(c.done)
	for {
		var msg cdpMessage
		err := websocket.JSON.Receive(c.ws, &msg)
		if err != nil {
			c.mu.Lock()
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
			return
		}
		if msg.ID != 0 && msg.Method == "" {
			c.mu.Lock()
			ch, ok := c.pending[msg.ID]
			if ok {
				delete(c.pending, msg.ID)
			}
			c.mu.Unlock()
			if ok {
				ch <- msg
			}
		}
	}
}

// Call sends a CDP method call and waits for the response (up to 30s timeout).
func (c *CDPClient) Call(method string, params any) (json.RawMessage, error) {
	id := int(c.nextID.Add(1))

	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshaling params: %w", err)
		}
		rawParams = b
	}

	msg := cdpMessage{ID: id, Method: method, Params: rawParams}

	ch := make(chan cdpMessage, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	if err := websocket.JSON.Send(c.ws, msg); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("sending CDP message: %w", err)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("CDP connection closed while waiting for response")
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-time.After(30 * time.Second):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("CDP call %s timed out after 30s", method)
	}
}

// Close shuts down the WebSocket connection.
func (c *CDPClient) Close() {
	_ = c.ws.Close()
	<-c.done
}
