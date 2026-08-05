package cdp

import (
	"context"
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

// cdpCallTimeoutDefault bounds a CDP call when the step carries no deadline of its own — the
// never-hang backstop for an ad-hoc invocation, not a policy about how long a page may take.
//
// cdpCallTimeoutFloor keeps a call from being cut to nothing when the step's budget is nearly
// spent: a 0ms wait would report "timed out" for a call that was never given a chance, which reads
// as a browser fault rather than an exhausted step.
const (
	cdpCallTimeoutDefault = 30 * time.Second
	cdpCallTimeoutFloor   = 5 * time.Second
)

// CDPClient is a lightweight Chrome DevTools Protocol WebSocket client.
type CDPClient struct {
	ws      *websocket.Conn
	ctx     context.Context
	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[int]chan cdpMessage
	done    chan struct{}
}

// NewCDPClient connects to a CDP WebSocket endpoint and starts reading messages. ctx is the STEP's
// context; every call this client makes is bounded by the budget remaining on it (see callTimeout).
func NewCDPClient(ctx context.Context, wsURL string) (*CDPClient, error) {
	ws, err := websocket.Dial(wsURL, "", "http://localhost")
	if err != nil {
		return nil, fmt.Errorf("connecting to CDP WebSocket %s: %w", wsURL, err)
	}

	if ctx == nil {
		ctx = context.Background()
	}
	c := &CDPClient{
		ws:      ws,
		ctx:     ctx,
		pending: make(map[int]chan cdpMessage),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

// callTimeout derives one call's bound from the STEP's remaining budget rather than a fixed
// constant. The fixed 30s it replaces made a step's own `eventually:` retry budget vacuous in
// exactly the case retries exist for: a wedged call cycle measured at 37.8s was guillotined at 30s
// every time, so a step given 90s to converge never got a single complete attempt.
//
// No deadline on the step → the default backstop. A deadline shorter than the floor → the floor, so
// a nearly-exhausted step still makes one honest attempt (the step ctx itself, selected on
// alongside this timer, remains the outer authority and will end the call regardless).
func (c *CDPClient) callTimeout() time.Duration {
	dl, ok := c.ctx.Deadline()
	if !ok {
		return cdpCallTimeoutDefault
	}
	if remaining := time.Until(dl); remaining > cdpCallTimeoutFloor {
		return remaining
	}
	return cdpCallTimeoutFloor
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

// Call sends a CDP method call and waits for the response, bounded by the step's remaining budget
// (callTimeout) and by the step context itself.
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

	timeout := c.callTimeout()
	abandon := func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
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
	case <-c.ctx.Done():
		abandon()
		return nil, fmt.Errorf("CDP call %s abandoned: step context ended (%v)", method, c.ctx.Err())
	case <-time.After(timeout):
		abandon()
		return nil, fmt.Errorf("CDP call %s timed out after %s", method, timeout.Round(time.Millisecond))
	}
}

// Close shuts down the WebSocket connection.
func (c *CDPClient) Close() {
	_ = c.ws.Close()
	<-c.done
}
