package spice

// Audio (playback/record) channels are NOT implemented in this fork: the cgo audio
// path — the ONLY consumer of the opus + portaudio bindings — was removed, so this
// library (and charly, which embeds it for the framebuffer / input / cursor / clipboard
// channels only and never touches audio) is unambiguously PURE Go: no libopus /
// libportaudio / libasound / libjack, no build tag, no cgo.
//
// These stubs let client.go compile unchanged: the Client struct keeps its
// `playback *ChPlayback` / `record *ChRecord` fields and its mute accessors, and the
// channel dispatch still calls setupPlayback / setupRecord — which return nil, so the
// audio channels are simply never connected. Every other channel (main, display, inputs,
// cursor, clipboard, webdav) is unaffected.

// ChPlayback is the (unimplemented) audio playback channel. The mute field exists only so
// client.go's mute accessors compile.
type ChPlayback struct {
	mute bool
}

// ChRecord is the (unimplemented) audio record channel.
type ChRecord struct{}

// setupPlayback / setupRecord are no-ops (audio is not implemented): they return nil so
// the dispatch leaves cl.playback / cl.record unset and the audio channels are never
// connected. No opus/portaudio symbols are referenced.
func (cl *Client) setupPlayback(id uint8) (*ChPlayback, error) { return nil, nil }
func (cl *Client) setupRecord(id uint8) (*ChRecord, error)     { return nil, nil }
