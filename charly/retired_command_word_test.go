package main

import (
	"testing"
)

func TestFirstCommandWord(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		want   string
		wantOK bool
	}{
		{"bare", nil, "", false},
		{"simple", []string{"check", "run", "x"}, "check", true},
		{"flag-before", []string{"--host", "o.example.org", "status"}, "status", true},
		{"flag-eq-value", []string{"--dir=/x/y", "deploy", "add"}, "deploy", true},
		{"value-flag-no-value", []string{"--host=", "version"}, "version", true},
		{"short-C", []string{"-C", "/p", "box", "build"}, "box", true},
		{"host-option", []string{"--host-option", "K=V", "logs"}, "logs", true},
		{"double-dash", []string{"--", "fleet", "add"}, "fleet", true},
		{"help", []string{"--help"}, "", false},
		{"retired-word", []string{"fleet", "add", "x"}, "fleet", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := firstCommandWord(c.args)
			if got != c.want || ok != c.wantOK {
				t.Fatalf("firstCommandWord(%v) = (%q, %v), want (%q, %v)", c.args, got, ok, c.want, c.wantOK)
			}
		})
	}
}
