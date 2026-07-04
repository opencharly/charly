package spice

// Thin wrapper over github.com/Shells-com/spice — ported verbatim from charly's
// former in-tree charly/spice_session.go, MINUS the SSH-tunnel field. The host
// pre-resolves the VM's live SPICE endpoint (go-libvirt, vm_target.go) and opens any
// qemu+ssh:// side tunnel itself, then hands this plugin a plain DIALABLE address
// (TCP host:port or a forwarded UNIX socket) via the check env — so the connection
// here is unconditionally local and needs no libvirt / SSH machinery.
//
// The upstream library needs:
//   - A Connector (we dial the resolved SPICE endpoint).
//   - A Driver (we capture display + cursor updates into fields we expose).
//   - A password (empty for AUTH_NONE; non-empty for SPICE_TICKET).
//
// It spins up goroutines for each SPICE channel (main, display, inputs, cursor,
// playback, record, webdav). Our Driver is a stub that doesn't render — it just
// stashes the latest framebuffer/cursor into mutex-guarded fields the methods read
// back. Audio channels are NOT implemented (the cgo audio path was removed entirely),
// so no opus/portaudio decoders are linked — the binary is pure Go.

import (
	"fmt"
	"image"
	"net"
	"sync"
	"time"

	spice "github.com/Shells-com/spice"
)

// spiceConnector satisfies spice.Connector by dialing a fixed SPICE endpoint. The
// endpoint can be a TCP host:port (classic) or a UNIX socket path (the modern
// default for charly-managed VMs after the socket-listen cutover). Compress is
// passed through as a hint to the library.
type spiceConnector struct {
	network string // "tcp" | "unix"
	addr    string
}

func (c *spiceConnector) SpiceConnect(compress bool) (net.Conn, error) {
	_ = compress
	return net.DialTimeout(c.network, c.addr, 10*time.Second)
}

// spiceDriver captures display/cursor updates into concurrent-safe fields. The
// methods read them back for the screenshot/cursor verbs.
type spiceDriver struct {
	mu         sync.Mutex
	displayImg image.Image
	cursorImg  image.Image
	cursorX    uint16
	cursorY    uint16
	inputsRef  *spice.ChInputs
	mainRef    *spice.ChMain
	clipWanted map[spice.SpiceClipboardSelection][]spice.SpiceClipboardFormat
}

func newSpiceDriver() *spiceDriver {
	return &spiceDriver{
		clipWanted: map[spice.SpiceClipboardSelection][]spice.SpiceClipboardFormat{},
	}
}

func (d *spiceDriver) DisplayInit(img image.Image) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.displayImg = img
}

func (d *spiceDriver) DisplayRefresh() {
	// No-op: we snapshot the image on demand via the screenshot method.
}

func (d *spiceDriver) SetEventsTarget(in *spice.ChInputs) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.inputsRef = in
}

func (d *spiceDriver) SetMainTarget(m *spice.ChMain) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.mainRef = m
}

func (d *spiceDriver) SetCursor(img image.Image, x, y uint16) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cursorImg = img
	d.cursorX = x
	d.cursorY = y
}

// Clipboard — minimal stubs. The CLI never wraps the agent-channel clipboard ops.
func (d *spiceDriver) ClipboardGrabbed(sel spice.SpiceClipboardSelection, types []spice.SpiceClipboardFormat) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.clipWanted[sel] = types
}
func (d *spiceDriver) ClipboardFetch(sel spice.SpiceClipboardSelection, t spice.SpiceClipboardFormat) ([]byte, error) {
	return nil, fmt.Errorf("opencharly spice driver does not provide clipboard data")
}
func (d *spiceDriver) ClipboardRelease(sel spice.SpiceClipboardSelection) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.clipWanted, sel)
}

// SpiceSession holds an open SPICE connection and provides the high-level verbs the
// methods use.
type SpiceSession struct {
	client *spice.Client
	driver *spiceDriver
	addr   string
}

// DialSpiceTCP opens a SPICE connection over TCP. Password empty = AUTH_NONE;
// non-empty = SPICE_TICKET. Blocks on the main-channel handshake + auth.
func DialSpiceTCP(host string, port int, passwd string) (*SpiceSession, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	conn := &spiceConnector{network: "tcp", addr: addr}
	drv := newSpiceDriver()
	cli, err := spice.New(conn, drv, passwd)
	if err != nil {
		return nil, fmt.Errorf("spice handshake with %s: %w", addr, err)
	}
	return &SpiceSession{client: cli, driver: drv, addr: addr}, nil
}

// DialSpiceUnix opens a SPICE connection over a UNIX socket — the default for
// charly-managed VMs after the socket-listen cutover.
func DialSpiceUnix(path, passwd string) (*SpiceSession, error) {
	conn := &spiceConnector{network: "unix", addr: path}
	drv := newSpiceDriver()
	cli, err := spice.New(conn, drv, passwd)
	if err != nil {
		return nil, fmt.Errorf("spice handshake with unix://%s: %w", path, err)
	}
	return &SpiceSession{client: cli, driver: drv, addr: "unix://" + path}, nil
}

// Display returns the latest framebuffer image, or nil if none received yet.
func (s *SpiceSession) Display() image.Image {
	s.driver.mu.Lock()
	defer s.driver.mu.Unlock()
	return s.driver.displayImg
}

// Cursor returns the current cursor bitmap + position.
func (s *SpiceSession) Cursor() (image.Image, uint16, uint16) {
	s.driver.mu.Lock()
	defer s.driver.mu.Unlock()
	return s.driver.cursorImg, s.driver.cursorX, s.driver.cursorY
}

// Inputs returns the inputs channel handle (may be nil if not yet set up).
func (s *SpiceSession) Inputs() *spice.ChInputs {
	s.driver.mu.Lock()
	defer s.driver.mu.Unlock()
	return s.driver.inputsRef
}

// pollUntil polls fn every 100ms until it returns true or the timeout elapses.
// The Shells-com/spice library populates the display/inputs fields ASYNCHRONOUSLY
// from per-channel goroutines with no readiness callback, so a bounded poll is the
// only synchronization the upstream API exposes — this is readiness polling on an
// async channel, not a flake-hiding retry (it mirrors the pollUntil the former
// in-tree session used).
func pollUntil(fn func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if fn() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// WaitForDisplay blocks (with timeout) until a framebuffer has been delivered.
func (s *SpiceSession) WaitForDisplay(timeout time.Duration) error {
	if !pollUntil(func() bool { return s.Display() != nil }, timeout) {
		return fmt.Errorf("no display frame within %s", timeout)
	}
	return nil
}

// WaitForInputs blocks until the inputs channel is ready.
func (s *SpiceSession) WaitForInputs(timeout time.Duration) error {
	if !pollUntil(func() bool { return s.Inputs() != nil }, timeout) {
		return fmt.Errorf("inputs channel not ready within %s", timeout)
	}
	return nil
}
