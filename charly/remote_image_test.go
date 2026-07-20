package main

import "testing"

func TestResolveImageName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"myapp", "myapp"},
		{"@github.com/org/repo/myapp:v1.0.0", "myapp"},
		{"simple-image", "simple-image"},
	}

	for _, tt := range tests {
		got := resolveBoxName(tt.input)
		if got != tt.want {
			t.Errorf("resolveBoxName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
