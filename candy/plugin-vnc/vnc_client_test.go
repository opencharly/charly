package main

import (
	"testing"
)

// These tests cover the RFB/VNC client moved from charly/vnc_client.go into this
// out-of-process plugin: the X11 keysym map, the button mask, the rune→keysym mapping,
// the VNC-auth bit reversal, and the VeNCrypt sub-type negotiation.

func TestVncKeyMap(t *testing.T) {
	tests := []struct {
		name string
		want uint32
	}{
		{"Return", 0xff0d},
		{"Escape", 0xff1b},
		{"Tab", 0xff09},
		{"BackSpace", 0xff08},
		{"Delete", 0xffff},
		{"Up", 0xff52},
		{"Down", 0xff54},
		{"Left", 0xff51},
		{"Right", 0xff53},
		{"F1", 0xffbe},
		{"F12", 0xffc9},
		{"Control_L", 0xffe3},
		{"Shift_L", 0xffe1},
		{"Alt_L", 0xffe9},
		{"Super_L", 0xffeb},
		{"space", 0x0020},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := vncKeyMap[tt.name]
			if !ok {
				t.Errorf("vncKeyMap[%q] not found", tt.name)
				return
			}
			if got != tt.want {
				t.Errorf("vncKeyMap[%q] = 0x%04x, want 0x%04x", tt.name, got, tt.want)
			}
		})
	}

	if _, ok := vncKeyMap["NonExistentKey"]; ok {
		t.Error("vncKeyMap should not contain 'NonExistentKey'")
	}
}

func TestVncButton(t *testing.T) {
	tests := []struct {
		name string
		want uint8
	}{
		{"left", vncButtonLeft},
		{"middle", vncButtonMiddle},
		{"right", vncButtonRight},
		{"unknown", vncButtonLeft},
		{"", vncButtonLeft},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vncButton(tt.name); got != tt.want {
				t.Errorf("vncButton(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestRuneToKeysym(t *testing.T) {
	tests := []struct {
		r    rune
		want uint32
		ok   bool
	}{
		{'a', 0x61, true},
		{'A', 0x41, true},
		{'0', 0x30, true},
		{' ', 0x20, true},
		{'\n', 0xff0d, true},
		{'\t', 0xff09, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.r), func(t *testing.T) {
			got, ok := runeToKeysym(tt.r)
			if ok != tt.ok || got != tt.want {
				t.Errorf("runeToKeysym(%q) = (0x%04x, %v), want (0x%04x, %v)", tt.r, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestReverseBits(t *testing.T) {
	// 0b10110000 -> 0b00001101
	if got := reverseBits(0xB0); got != 0x0D {
		t.Errorf("reverseBits(0xB0) = 0x%02X, want 0x0D", got)
	}
	if got := reverseBits(0x00); got != 0x00 {
		t.Errorf("reverseBits(0x00) = 0x%02X, want 0x00", got)
	}
	if got := reverseBits(0xFF); got != 0xFF {
		t.Errorf("reverseBits(0xFF) = 0x%02X, want 0xFF", got)
	}
}

func TestChooseVeNCryptSubType(t *testing.T) {
	allTypes := []uint32{vencryptTLSNone, vencryptTLSVnc, vencryptTLSPlain, vencryptX509None, vencryptX509Vnc, vencryptX509Plain}

	// With password: prefer TLSPlain
	if got := chooseVeNCryptSubType(allTypes, "secret"); got != vencryptTLSPlain {
		t.Errorf("with password, all types: got %d, want %d (TLSPlain)", got, vencryptTLSPlain)
	}

	// With password, only TLSVnc available
	if got := chooseVeNCryptSubType([]uint32{vencryptTLSNone, vencryptTLSVnc}, "secret"); got != vencryptTLSVnc {
		t.Errorf("with password, TLSNone+TLSVnc: got %d, want %d (TLSVnc)", got, vencryptTLSVnc)
	}

	// Without password: prefer TLSNone
	if got := chooseVeNCryptSubType(allTypes, ""); got != vencryptTLSNone {
		t.Errorf("no password, all types: got %d, want %d (TLSNone)", got, vencryptTLSNone)
	}

	// Without password, only X509None available
	if got := chooseVeNCryptSubType([]uint32{vencryptX509None}, ""); got != vencryptX509None {
		t.Errorf("no password, X509None only: got %d, want %d (X509None)", got, vencryptX509None)
	}

	// No supported types
	if got := chooseVeNCryptSubType([]uint32{999}, "secret"); got != 0 {
		t.Errorf("unsupported types: got %d, want 0", got)
	}

	// Empty list
	if got := chooseVeNCryptSubType(nil, ""); got != 0 {
		t.Errorf("empty list: got %d, want 0", got)
	}
}
