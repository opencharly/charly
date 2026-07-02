package main

import (
	"bytes"
	"compress/zlib"
	"crypto/des"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"io"
	"net"
	"slices"
	"strings"
	"time"
)

// VNCClient implements a minimal RFC 6143 VNC client.
type VNCClient struct {
	conn        net.Conn
	width       uint16
	height      uint16
	name        string
	pixelFormat vncPixelFormat
	zBuf        *bytes.Buffer // persistent zlib input buffer for ZRLE
	zReader     io.ReadCloser // persistent zlib decompressor for ZRLE
}

// vncPixelFormat represents the RFB pixel format (16 bytes on wire).
type vncPixelFormat struct {
	BPP        uint8
	Depth      uint8
	BigEndian  uint8
	TrueColor  uint8
	RedMax     uint16
	GreenMax   uint16
	BlueMax    uint16
	RedShift   uint8
	GreenShift uint8
	BlueShift  uint8
	_          [3]byte
}

// NewVNCClient connects to a VNC server and performs the RFB handshake.
func NewVNCClient(address, password string) (*VNCClient, error) {
	conn, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connecting to VNC server %s: %w", address, err)
	}

	c := &VNCClient{conn: conn}
	if err := c.handshake(password); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("VNC handshake with %s: %w", address, err)
	}
	return c, nil
}

// handshake performs the full RFB handshake (version, security, init).
func (c *VNCClient) handshake(password string) error {
	// §7.1.1 Protocol Version
	var serverVersion [12]byte
	if _, err := io.ReadFull(c.conn, serverVersion[:]); err != nil {
		return fmt.Errorf("reading server version: %w", err)
	}
	var major, minor uint
	if _, err := fmt.Sscanf(string(serverVersion[:]), "RFB %d.%d\n", &major, &minor); err != nil {
		return fmt.Errorf("parsing server version %q: %w", serverVersion, err)
	}

	// We always speak 3.8.
	clientVersion := "RFB 003.008\n"
	if _, err := c.conn.Write([]byte(clientVersion)); err != nil {
		return fmt.Errorf("sending client version: %w", err)
	}

	// §7.1.2 Security Handshake (RFB 3.8)
	var numSecTypes uint8
	if err := binary.Read(c.conn, binary.BigEndian, &numSecTypes); err != nil {
		return fmt.Errorf("reading security types count: %w", err)
	}
	if numSecTypes == 0 {
		return c.readErrorReason("security handshake")
	}
	secTypes := make([]uint8, numSecTypes)
	if _, err := io.ReadFull(c.conn, secTypes); err != nil {
		return fmt.Errorf("reading security types: %w", err)
	}

	// Choose security type: prefer VeNCrypt (19) when available, then None (1), then VNC auth (2).
	var chosenType uint8
	hasVeNCrypt := slices.Contains(secTypes, 19)
	if hasVeNCrypt {
		chosenType = 19
	} else {
		if slices.Contains(secTypes, 1) {
			chosenType = 1
		}
		if chosenType == 0 {
			if slices.Contains(secTypes, 2) {
				chosenType = 2
			}
		}
	}
	if chosenType == 0 {
		return fmt.Errorf("no supported security type (server offers: %v)", secTypes)
	}

	// Send chosen type.
	if err := binary.Write(c.conn, binary.BigEndian, chosenType); err != nil {
		return fmt.Errorf("sending security type: %w", err)
	}

	// Perform auth handshake based on chosen type.
	switch chosenType {
	case 1: // None — SecurityResult follows
		var secResult uint32
		if err := binary.Read(c.conn, binary.BigEndian, &secResult); err != nil {
			return fmt.Errorf("reading security result: %w", err)
		}
		if secResult != 0 {
			return c.readErrorReason("authentication failed")
		}
	case 2: // VNC auth
		if password == "" {
			return fmt.Errorf("VNC server requires authentication but no password is provisioned (provision wayvnc auth at deploy time, or set VNC_PASSWORD)")
		}
		if err := c.vncAuth(password); err != nil {
			return err
		}
		var secResult uint32
		if err := binary.Read(c.conn, binary.BigEndian, &secResult); err != nil {
			return fmt.Errorf("reading security result: %w", err)
		}
		if secResult != 0 {
			return c.readErrorReason("authentication failed")
		}
	case 19: // VeNCrypt
		if err := c.vencryptHandshake(password); err != nil {
			return err
		}
	}

	// §7.3.1 ClientInit (shared=true)
	if err := binary.Write(c.conn, binary.BigEndian, uint8(1)); err != nil {
		return fmt.Errorf("sending ClientInit: %w", err)
	}

	// §7.3.2 ServerInit
	var serverInit struct {
		Width      uint16
		Height     uint16
		PixelFmt   vncPixelFormat
		NameLength uint32
	}
	if err := binary.Read(c.conn, binary.BigEndian, &serverInit); err != nil {
		return fmt.Errorf("reading ServerInit: %w", err)
	}
	c.width = serverInit.Width
	c.height = serverInit.Height
	c.pixelFormat = serverInit.PixelFmt

	nameBuf := make([]byte, serverInit.NameLength)
	if _, err := io.ReadFull(c.conn, nameBuf); err != nil {
		return fmt.Errorf("reading desktop name: %w", err)
	}
	c.name = string(nameBuf)

	// Send SetPixelFormat: request 32-bit BGRA (standard, non-overlapping).
	pf := vncPixelFormat{
		BPP:        32,
		Depth:      24,
		BigEndian:  0, // little-endian
		TrueColor:  1,
		RedMax:     255,
		GreenMax:   255,
		BlueMax:    255,
		RedShift:   16,
		GreenShift: 8,
		BlueShift:  0,
	}
	if err := c.sendSetPixelFormat(pf); err != nil {
		return err
	}
	c.pixelFormat = pf

	// Send SetEncodings: prefer ZRLE (compressed), fall back to Raw.
	if err := c.sendSetEncodings([]int32{16, 0}); err != nil { // 16 = ZRLE, 0 = Raw
		return err
	}

	return nil
}

// vncAuth performs VNC DES challenge-response authentication.
func (c *VNCClient) vncAuth(password string) error {
	var challenge [16]byte
	if _, err := io.ReadFull(c.conn, challenge[:]); err != nil {
		return fmt.Errorf("reading VNC auth challenge: %w", err)
	}

	// VNC uses a reversed-bit DES key derived from the password.
	key := make([]byte, 8)
	copy(key, password)
	for i := range key {
		key[i] = reverseBits(key[i])
	}

	cipher, err := des.NewCipher(key)
	if err != nil {
		return fmt.Errorf("creating DES cipher: %w", err)
	}
	for i := 0; i < 16; i += cipher.BlockSize() {
		cipher.Encrypt(challenge[i:i+cipher.BlockSize()], challenge[i:i+cipher.BlockSize()])
	}

	if _, err := c.conn.Write(challenge[:]); err != nil {
		return fmt.Errorf("sending VNC auth response: %w", err)
	}
	return nil
}

// VeNCrypt security type constants.
const (
	vencryptTLSNone   uint32 = 257
	vencryptTLSVnc    uint32 = 258
	vencryptTLSPlain  uint32 = 259
	vencryptX509None  uint32 = 260
	vencryptX509Vnc   uint32 = 261
	vencryptX509Plain uint32 = 262
)

// vencryptHandshake performs VeNCrypt (security type 19) negotiation.
//
//nolint:gocyclo // VeNCrypt handshake (RFC 6143): sequential version/subtype/TLS negotiation steps depend on prior state; extraction breaks protocol flow
func (c *VNCClient) vencryptHandshake(password string) error {
	// Step 1: Version negotiation.
	var serverMajor, serverMinor uint8
	if err := binary.Read(c.conn, binary.BigEndian, &serverMajor); err != nil {
		return fmt.Errorf("VeNCrypt: reading server version major: %w", err)
	}
	if err := binary.Read(c.conn, binary.BigEndian, &serverMinor); err != nil {
		return fmt.Errorf("VeNCrypt: reading server version minor: %w", err)
	}
	// We support version 0.2.
	if err := binary.Write(c.conn, binary.BigEndian, uint8(0)); err != nil {
		return fmt.Errorf("VeNCrypt: sending version major: %w", err)
	}
	if err := binary.Write(c.conn, binary.BigEndian, uint8(2)); err != nil {
		return fmt.Errorf("VeNCrypt: sending version minor: %w", err)
	}

	// Step 2: Server confirms version.
	var versionStatus uint8
	if err := binary.Read(c.conn, binary.BigEndian, &versionStatus); err != nil {
		return fmt.Errorf("VeNCrypt: reading version status: %w", err)
	}
	if versionStatus != 0 {
		return fmt.Errorf("VeNCrypt: server rejected version 0.2 (status=%d)", versionStatus)
	}

	// Step 3: Read available sub-types.
	var numSubTypes uint8
	if err := binary.Read(c.conn, binary.BigEndian, &numSubTypes); err != nil {
		return fmt.Errorf("VeNCrypt: reading subtype count: %w", err)
	}
	subTypes := make([]uint32, numSubTypes)
	for i := range subTypes {
		if err := binary.Read(c.conn, binary.BigEndian, &subTypes[i]); err != nil {
			return fmt.Errorf("VeNCrypt: reading subtype %d: %w", i, err)
		}
	}

	// Step 4: Choose sub-type based on whether we have a password.
	chosenSubType := chooseVeNCryptSubType(subTypes, password)
	if chosenSubType == 0 {
		return fmt.Errorf("VeNCrypt: no supported subtype (server offers: %v)", subTypes)
	}

	if err := binary.Write(c.conn, binary.BigEndian, chosenSubType); err != nil {
		return fmt.Errorf("VeNCrypt: sending chosen subtype: %w", err)
	}

	// Step 5: Server confirms subtype selection (1 = OK in VeNCrypt).
	var subTypeStatus uint8
	if err := binary.Read(c.conn, binary.BigEndian, &subTypeStatus); err != nil {
		return fmt.Errorf("VeNCrypt: reading subtype status: %w", err)
	}
	if subTypeStatus != 1 {
		return fmt.Errorf("VeNCrypt: server rejected subtype %d (status=%d)", chosenSubType, subTypeStatus)
	}

	// Step 6: TLS handshake on existing connection.
	tlsConn := tls.Client(c.conn, &tls.Config{
		InsecureSkipVerify: true, // wayvnc uses self-signed certs
	})
	if err := tlsConn.Handshake(); err != nil {
		return fmt.Errorf("VeNCrypt: TLS handshake: %w", err)
	}
	// Replace the connection with the TLS-wrapped one.
	c.conn = tlsConn

	// Step 7: Perform sub-authentication inside TLS tunnel.
	switch chosenSubType {
	case vencryptTLSNone, vencryptX509None:
		// No further auth needed. Read SecurityResult.
		var secResult uint32
		if err := binary.Read(c.conn, binary.BigEndian, &secResult); err != nil {
			return fmt.Errorf("VeNCrypt: reading security result: %w", err)
		}
		if secResult != 0 {
			return c.readErrorReason("VeNCrypt authentication failed")
		}
	case vencryptTLSVnc, vencryptX509Vnc:
		// VNC DES challenge-response inside TLS.
		if password == "" {
			return fmt.Errorf("VNC server requires authentication but no password is provisioned (provision wayvnc auth at deploy time, or set VNC_PASSWORD)")
		}
		if err := c.vncAuth(password); err != nil {
			return err
		}
		var secResult uint32
		if err := binary.Read(c.conn, binary.BigEndian, &secResult); err != nil {
			return fmt.Errorf("VeNCrypt: reading security result: %w", err)
		}
		if secResult != 0 {
			return c.readErrorReason("VeNCrypt VNC authentication failed")
		}
	case vencryptTLSPlain, vencryptX509Plain:
		// Plain username/password inside TLS.
		if password == "" {
			return fmt.Errorf("VNC server requires authentication but no password is provisioned (provision wayvnc auth at deploy time, or set VNC_PASSWORD)")
		}
		username := "user"
		if err := binary.Write(c.conn, binary.BigEndian, uint32(len(username))); err != nil {
			return fmt.Errorf("VeNCrypt: sending username length: %w", err)
		}
		if err := binary.Write(c.conn, binary.BigEndian, uint32(len(password))); err != nil {
			return fmt.Errorf("VeNCrypt: sending password length: %w", err)
		}
		if _, err := c.conn.Write([]byte(username)); err != nil {
			return fmt.Errorf("VeNCrypt: sending username: %w", err)
		}
		if _, err := c.conn.Write([]byte(password)); err != nil {
			return fmt.Errorf("VeNCrypt: sending password: %w", err)
		}
		var secResult uint32
		if err := binary.Read(c.conn, binary.BigEndian, &secResult); err != nil {
			return fmt.Errorf("VeNCrypt: reading security result: %w", err)
		}
		if secResult != 0 {
			return c.readErrorReason("VeNCrypt plain authentication failed")
		}
	}

	return nil
}

// chooseVeNCryptSubType selects the best VeNCrypt sub-type from the offered list.
func chooseVeNCryptSubType(subTypes []uint32, password string) uint32 {
	if password != "" {
		// With password: prefer TLSPlain > TLSVnc > X509Plain > X509Vnc
		for _, pref := range []uint32{vencryptTLSPlain, vencryptTLSVnc, vencryptX509Plain, vencryptX509Vnc} {
			for _, st := range subTypes {
				if st == pref {
					return st
				}
			}
		}
	}
	// Without password (or no auth subtypes matched): prefer TLSNone > X509None
	for _, pref := range []uint32{vencryptTLSNone, vencryptX509None} {
		for _, st := range subTypes {
			if st == pref {
				return st
			}
		}
	}
	// Fallback: try any supported subtype
	for _, st := range subTypes {
		switch st {
		case vencryptTLSNone, vencryptTLSVnc, vencryptTLSPlain,
			vencryptX509None, vencryptX509Vnc, vencryptX509Plain:
			return st
		}
	}
	return 0
}

func reverseBits(b byte) byte {
	b = (b&0x55)<<1 | (b&0xAA)>>1
	b = (b&0x33)<<2 | (b&0xCC)>>2
	b = (b&0x0F)<<4 | (b&0xF0)>>4
	return b
}

func (c *VNCClient) readErrorReason(context string) error {
	var reasonLen uint32
	if err := binary.Read(c.conn, binary.BigEndian, &reasonLen); err != nil {
		return fmt.Errorf("%s: reading error reason length: %w", context, err)
	}
	reason := make([]byte, reasonLen)
	if _, err := io.ReadFull(c.conn, reason); err != nil {
		return fmt.Errorf("%s: reading error reason: %w", context, err)
	}
	return fmt.Errorf("%s: %s", context, reason)
}

// --- Client-to-server messages ---

func (c *VNCClient) sendSetPixelFormat(pf vncPixelFormat) error {
	msg := struct {
		MsgType uint8
		_       [3]byte
		PF      vncPixelFormat
	}{MsgType: 0}
	msg.PF = pf
	return binary.Write(c.conn, binary.BigEndian, msg)
}

func (c *VNCClient) sendSetEncodings(encodings []int32) error {
	header := struct {
		MsgType uint8
		_       byte
		Num     uint16
	}{MsgType: 2, Num: uint16(len(encodings))}
	if err := binary.Write(c.conn, binary.BigEndian, header); err != nil {
		return err
	}
	return binary.Write(c.conn, binary.BigEndian, encodings)
}

// FramebufferUpdateRequest sends §7.5.3.
func (c *VNCClient) FramebufferUpdateRequest(incremental bool, x, y, w, h uint16) error {
	inc := uint8(0)
	if incremental {
		inc = 1
	}
	msg := struct {
		MsgType uint8
		Inc     uint8
		X, Y    uint16
		W, H    uint16
	}{MsgType: 3, Inc: inc, X: x, Y: y, W: w, H: h}
	return binary.Write(c.conn, binary.BigEndian, msg)
}

// KeyEvent sends §7.5.4.
func (c *VNCClient) KeyEvent(key uint32, down bool) error {
	d := uint8(0)
	if down {
		d = 1
	}
	msg := struct {
		MsgType uint8
		Down    uint8
		_       [2]byte
		Key     uint32
	}{MsgType: 4, Down: d, Key: key}
	return binary.Write(c.conn, binary.BigEndian, msg)
}

// PointerEvent sends §7.5.5.
func (c *VNCClient) PointerEvent(mask uint8, x, y uint16) error {
	msg := struct {
		MsgType uint8
		Mask    uint8
		X, Y    uint16
	}{MsgType: 5, Mask: mask, X: x, Y: y}
	return binary.Write(c.conn, binary.BigEndian, msg)
}

// ClientCutText sends §7.5.6.
func (c *VNCClient) ClientCutText(text string) error {
	header := struct {
		MsgType uint8
		_       [3]byte
		Length  uint32
	}{MsgType: 6, Length: uint32(len(text))}
	if err := binary.Write(c.conn, binary.BigEndian, header); err != nil {
		return err
	}
	_, err := c.conn.Write([]byte(text))
	return err
}

// --- High-level operations ---

// KeyPress sends a key down + up.
func (c *VNCClient) KeyPress(key uint32) error {
	if err := c.KeyEvent(key, true); err != nil {
		return fmt.Errorf("key press: %w", err)
	}
	if err := c.KeyEvent(key, false); err != nil {
		return fmt.Errorf("key release: %w", err)
	}
	return nil
}

// TypeText sends key press/release for each character.
func (c *VNCClient) TypeText(text string) error {
	for _, r := range text {
		key, ok := runeToKeysym(r)
		if !ok {
			return fmt.Errorf("unsupported character %q (U+%04X)", r, r)
		}
		if err := c.KeyPress(key); err != nil {
			return err
		}
	}
	return nil
}

// PointerClick moves to (x,y) first to trigger virtual pointer creation and
// wl_seat.capabilities broadcast, waits for Wayland clients to bind wl_pointer,
// then sends press + release with realistic timing.
func (c *VNCClient) PointerClick(x, y uint16, buttonMask uint8) error {
	if err := c.PointerEvent(0, x, y); err != nil {
		return fmt.Errorf("pointer move: %w", err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := c.PointerEvent(buttonMask, x, y); err != nil {
		return fmt.Errorf("pointer press: %w", err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := c.PointerEvent(0, x, y); err != nil {
		return fmt.Errorf("pointer release: %w", err)
	}
	return nil
}

// PointerMove moves without clicking.
func (c *VNCClient) PointerMove(x, y uint16) error {
	if err := c.PointerEvent(0, x, y); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	return nil
}

// Screenshot captures the framebuffer as an image.
func (c *VNCClient) Screenshot() (image.Image, error) {
	if err := c.FramebufferUpdateRequest(false, 0, 0, c.width, c.height); err != nil {
		return nil, fmt.Errorf("requesting framebuffer: %w", err)
	}

	_ = c.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	defer c.conn.SetReadDeadline(time.Time{}) //nolint:errcheck

	// Read server messages until we get a FramebufferUpdate.
	for {
		var msgType uint8
		if err := binary.Read(c.conn, binary.BigEndian, &msgType); err != nil {
			return nil, fmt.Errorf("reading server message: %w", err)
		}

		switch msgType {
		case 0: // FramebufferUpdate
			return c.readFramebufferUpdate()
		case 1: // SetColorMapEntries
			if err := c.skipSetColorMapEntries(); err != nil {
				return nil, err
			}
		case 2: // Bell - no payload
			continue
		case 3: // ServerCutText
			if err := c.skipServerCutText(); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unknown server message type: %d", msgType)
		}
	}
}

func (c *VNCClient) readFramebufferUpdate() (image.Image, error) {
	var header struct {
		_       byte
		NumRect uint16
	}
	if err := binary.Read(c.conn, binary.BigEndian, &header); err != nil {
		return nil, fmt.Errorf("reading framebuffer update header: %w", err)
	}

	img := image.NewRGBA(image.Rect(0, 0, int(c.width), int(c.height)))
	bpp := int(c.pixelFormat.BPP) / 8 // bytes per pixel

	for i := 0; i < int(header.NumRect); i++ {
		var rect struct {
			X, Y          uint16
			Width, Height uint16
			Encoding      int32
		}
		if err := binary.Read(c.conn, binary.BigEndian, &rect); err != nil {
			return nil, fmt.Errorf("reading rectangle header: %w", err)
		}

		switch rect.Encoding {
		case 0: // Raw
			pixelData := make([]byte, int(rect.Width)*int(rect.Height)*bpp)
			if _, err := io.ReadFull(c.conn, pixelData); err != nil {
				return nil, fmt.Errorf("reading pixel data: %w", err)
			}
			for py := 0; py < int(rect.Height); py++ {
				for px := 0; px < int(rect.Width); px++ {
					offset := (py*int(rect.Width) + px) * bpp
					pixel := pixelData[offset : offset+bpp]
					var r, g, b uint8
					var val uint32
					switch bpp {
					case 4:
						if c.pixelFormat.BigEndian != 0 {
							val = binary.BigEndian.Uint32(pixel)
						} else {
							val = binary.LittleEndian.Uint32(pixel)
						}
					case 2:
						if c.pixelFormat.BigEndian != 0 {
							val = uint32(binary.BigEndian.Uint16(pixel))
						} else {
							val = uint32(binary.LittleEndian.Uint16(pixel))
						}
					case 1:
						val = uint32(pixel[0])
					}
					r = uint8((val >> c.pixelFormat.RedShift) & uint32(c.pixelFormat.RedMax))
					g = uint8((val >> c.pixelFormat.GreenShift) & uint32(c.pixelFormat.GreenMax))
					b = uint8((val >> c.pixelFormat.BlueShift) & uint32(c.pixelFormat.BlueMax))
					img.SetRGBA(int(rect.X)+px, int(rect.Y)+py, color.RGBA{R: r, G: g, B: b, A: 255})
				}
			}
		case 16: // ZRLE
			if err := c.decodeZRLE(img, rect.X, rect.Y, rect.Width, rect.Height); err != nil {
				return nil, fmt.Errorf("decoding ZRLE rectangle: %w", err)
			}
		default:
			return nil, fmt.Errorf("unsupported encoding: %d", rect.Encoding)
		}
	}

	return img, nil
}

func (c *VNCClient) skipSetColorMapEntries() error {
	var header struct {
		_          byte
		FirstColor uint16
		NumColors  uint16
	}
	if err := binary.Read(c.conn, binary.BigEndian, &header); err != nil {
		return err
	}
	// Each color is 6 bytes (R, G, B as uint16).
	skip := make([]byte, int(header.NumColors)*6)
	_, err := io.ReadFull(c.conn, skip)
	return err
}

func (c *VNCClient) skipServerCutText() error {
	var header struct {
		_      [3]byte
		Length uint32
	}
	if err := binary.Read(c.conn, binary.BigEndian, &header); err != nil {
		return err
	}
	skip := make([]byte, header.Length)
	_, err := io.ReadFull(c.conn, skip)
	return err
}

// Width returns the framebuffer width.
func (c *VNCClient) Width() uint16 { return c.width }

// Height returns the framebuffer height.
func (c *VNCClient) Height() uint16 { return c.height }

// DesktopName returns the VNC desktop name.
func (c *VNCClient) DesktopName() string { return c.name }

// Close disconnects from the VNC server. The host owns the venue forward/tunnel (it
// is torn down by the preresolve cleanup AFTER this Invoke), so the plugin only closes
// its own TCP connection + zlib reader.
func (c *VNCClient) Close() error {
	if c.zReader != nil {
		c.zReader.Close()
	}
	return c.conn.Close()
}

// --- ZRLE encoding support (RFC 6143 §7.7.6) ---

// cpixelSize returns the CPIXEL size in bytes for ZRLE encoding.
// When BPP=32, Depth=24, TrueColor=1, CPIXEL is 3 bytes.
func (c *VNCClient) cpixelSize() int {
	if c.pixelFormat.BPP == 32 && c.pixelFormat.Depth == 24 && c.pixelFormat.TrueColor == 1 {
		return 3
	}
	return int(c.pixelFormat.BPP) / 8
}

// readCPixel reads a CPIXEL from r and returns it as color.RGBA.
func (c *VNCClient) readCPixel(r io.Reader, cpLen int) (color.RGBA, error) {
	buf := make([]byte, cpLen)
	if _, err := io.ReadFull(r, buf); err != nil {
		return color.RGBA{}, err
	}
	if cpLen == 3 {
		// CPIXEL: least significant 3 bytes (little-endian).
		// With RedShift=16, GreenShift=8, BlueShift=0: bytes are B, G, R.
		return color.RGBA{R: buf[2], G: buf[1], B: buf[0], A: 255}, nil
	}
	var val uint32
	switch cpLen {
	case 4:
		if c.pixelFormat.BigEndian != 0 {
			val = binary.BigEndian.Uint32(buf)
		} else {
			val = binary.LittleEndian.Uint32(buf)
		}
	case 2:
		if c.pixelFormat.BigEndian != 0 {
			val = uint32(binary.BigEndian.Uint16(buf))
		} else {
			val = uint32(binary.LittleEndian.Uint16(buf))
		}
	case 1:
		val = uint32(buf[0])
	}
	return color.RGBA{
		R: uint8((val >> c.pixelFormat.RedShift) & uint32(c.pixelFormat.RedMax)),
		G: uint8((val >> c.pixelFormat.GreenShift) & uint32(c.pixelFormat.GreenMax)),
		B: uint8((val >> c.pixelFormat.BlueShift) & uint32(c.pixelFormat.BlueMax)),
		A: 255,
	}, nil
}

// decodeZRLE decodes a ZRLE-encoded rectangle.
func (c *VNCClient) decodeZRLE(img *image.RGBA, rx, ry, rw, rh uint16) error {
	var length uint32
	if err := binary.Read(c.conn, binary.BigEndian, &length); err != nil {
		return fmt.Errorf("reading ZRLE length: %w", err)
	}
	compressed := make([]byte, length)
	if _, err := io.ReadFull(c.conn, compressed); err != nil {
		return fmt.Errorf("reading ZRLE data: %w", err)
	}

	// The zlib stream is persistent across all ZRLE rectangles in a connection.
	if c.zBuf == nil {
		c.zBuf = new(bytes.Buffer)
	}
	c.zBuf.Write(compressed)

	if c.zReader == nil {
		r, err := zlib.NewReader(c.zBuf)
		if err != nil {
			return fmt.Errorf("zlib init: %w", err)
		}
		c.zReader = r
	}

	cpLen := c.cpixelSize()

	for ty := int(ry); ty < int(ry)+int(rh); ty += 64 {
		th := int(ry) + int(rh) - ty
		if th > 64 {
			th = 64
		}
		for tx := int(rx); tx < int(rx)+int(rw); tx += 64 {
			tw := int(rx) + int(rw) - tx
			if tw > 64 {
				tw = 64
			}
			if err := c.decodeZRLETile(img, tx, ty, tw, th, cpLen); err != nil {
				return fmt.Errorf("tile at (%d,%d): %w", tx, ty, err)
			}
		}
	}
	return nil
}

// decodeZRLETile decodes a single ZRLE tile from the decompressed stream.
//
//nolint:gocyclo // ZRLE tile decoder (RFC 6143 §7.7.6): 5 encoding branches (raw/solid/packed-palette/RLE/palette-RLE) with distinct bit-unpacking; each is a complete codec variant
func (c *VNCClient) decodeZRLETile(img *image.RGBA, tx, ty, tw, th, cpLen int) error {
	var subenc uint8
	if err := binary.Read(c.zReader, binary.BigEndian, &subenc); err != nil {
		return fmt.Errorf("reading subencoding: %w", err)
	}

	numPixels := tw * th

	switch {
	case subenc == 0: // Raw CPIXEL
		for py := range th {
			for px := range tw {
				clr, err := c.readCPixel(c.zReader, cpLen)
				if err != nil {
					return err
				}
				img.SetRGBA(tx+px, ty+py, clr)
			}
		}

	case subenc == 1: // Solid color
		clr, err := c.readCPixel(c.zReader, cpLen)
		if err != nil {
			return err
		}
		for py := range th {
			for px := range tw {
				img.SetRGBA(tx+px, ty+py, clr)
			}
		}

	case subenc >= 2 && subenc <= 16: // Packed palette
		paletteSize := int(subenc)
		palette := make([]color.RGBA, paletteSize)
		for i := range palette {
			clr, err := c.readCPixel(c.zReader, cpLen)
			if err != nil {
				return err
			}
			palette[i] = clr
		}
		var bitsPerIndex int
		switch {
		case paletteSize == 2:
			bitsPerIndex = 1
		case paletteSize <= 4:
			bitsPerIndex = 2
		default:
			bitsPerIndex = 4
		}
		for py := range th {
			bytesPerRow := (tw*bitsPerIndex + 7) / 8
			rowBuf := make([]byte, bytesPerRow)
			if _, err := io.ReadFull(c.zReader, rowBuf); err != nil {
				return err
			}
			for px := range tw {
				bitPos := px * bitsPerIndex
				byteIdx := bitPos / 8
				bitOff := uint(8 - bitsPerIndex - (bitPos % 8))
				mask := byte((1 << bitsPerIndex) - 1)
				idx := int((rowBuf[byteIdx] >> bitOff) & mask)
				if idx >= paletteSize {
					idx = 0
				}
				img.SetRGBA(tx+px, ty+py, palette[idx])
			}
		}

	case subenc == 128: // Plain RLE
		pos := 0
		for pos < numPixels {
			clr, err := c.readCPixel(c.zReader, cpLen)
			if err != nil {
				return err
			}
			runLen := 1
			for {
				var b uint8
				if err := binary.Read(c.zReader, binary.BigEndian, &b); err != nil {
					return err
				}
				runLen += int(b)
				if b != 255 {
					break
				}
			}
			for i := 0; i < runLen && pos < numPixels; i++ {
				img.SetRGBA(tx+pos%tw, ty+pos/tw, clr)
				pos++
			}
		}

	case subenc >= 129: // Palette RLE
		paletteSize := int(subenc - 128)
		palette := make([]color.RGBA, paletteSize)
		for i := range palette {
			clr, err := c.readCPixel(c.zReader, cpLen)
			if err != nil {
				return err
			}
			palette[i] = clr
		}
		pos := 0
		for pos < numPixels {
			var idx uint8
			if err := binary.Read(c.zReader, binary.BigEndian, &idx); err != nil {
				return err
			}
			if idx >= 128 {
				palIdx := int(idx - 128)
				if palIdx >= paletteSize {
					palIdx = 0
				}
				runLen := 1
				for {
					var b uint8
					if err := binary.Read(c.zReader, binary.BigEndian, &b); err != nil {
						return err
					}
					runLen += int(b)
					if b != 255 {
						break
					}
				}
				for i := 0; i < runLen && pos < numPixels; i++ {
					img.SetRGBA(tx+pos%tw, ty+pos/tw, palette[palIdx])
					pos++
				}
			} else {
				palIdx := int(idx)
				if palIdx >= paletteSize {
					palIdx = 0
				}
				img.SetRGBA(tx+pos%tw, ty+pos/tw, palette[palIdx])
				pos++
			}
		}

	default: // 17-127 are unused per RFC 6143
		return fmt.Errorf("unsupported ZRLE subencoding: %d", subenc)
	}
	return nil
}

// --- Key name mapping ---

var vncKeyMap = map[string]uint32{
	"Return":    0xff0d,
	"Escape":    0xff1b,
	"Tab":       0xff09,
	"BackSpace": 0xff08,
	"Delete":    0xffff,
	"Home":      0xff50,
	"End":       0xff57,
	"Page_Up":   0xff55,
	"Page_Down": 0xff56,
	"Up":        0xff52,
	"Down":      0xff54,
	"Left":      0xff51,
	"Right":     0xff53,
	"Insert":    0xff63,
	"F1":        0xffbe,
	"F2":        0xffbf,
	"F3":        0xffc0,
	"F4":        0xffc1,
	"F5":        0xffc2,
	"F6":        0xffc3,
	"F7":        0xffc4,
	"F8":        0xffc5,
	"F9":        0xffc6,
	"F10":       0xffc7,
	"F11":       0xffc8,
	"F12":       0xffc9,
	"Shift_L":   0xffe1,
	"Shift_R":   0xffe2,
	"Control_L": 0xffe3,
	"Control_R": 0xffe4,
	"Alt_L":     0xffe9,
	"Alt_R":     0xffea,
	"Super_L":   0xffeb,
	"Super_R":   0xffec,
	"Meta_L":    0xffe7,
	"Meta_R":    0xffe8,
	"Caps_Lock": 0xffe5,
	"space":     0x0020,
}

func vncKeyNames() string {
	names := make([]string, 0, len(vncKeyMap))
	for k := range vncKeyMap {
		names = append(names, k)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// runeToKeysym converts a rune to an X11 keysym.
func runeToKeysym(r rune) (uint32, bool) {
	switch r {
	case '\n':
		return 0xff0d, true // Return
	case '\t':
		return 0xff09, true // Tab
	case '\b':
		return 0xff08, true // BackSpace
	case '\r':
		return 0xff0d, true // Return
	}
	if r >= 0x20 && r <= 0xFF {
		return uint32(r), true
	}
	return 0, false
}

// --- Button constants ---

const (
	vncButtonLeft   = 1
	vncButtonMiddle = 2
	vncButtonRight  = 4
)

func vncButton(name string) uint8 {
	switch name {
	case "left":
		return vncButtonLeft
	case "middle":
		return vncButtonMiddle
	case "right":
		return vncButtonRight
	default:
		return vncButtonLeft
	}
}
