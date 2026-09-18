package httpserver

import "testing"

func TestOriginAllowed(t *testing.T) {
	tests := []struct {
		origin, bind string
		ok           bool
	}{
		{"", "", true},
		{"null", "", true},
		{"http://localhost:1234", "", true},
		{"http://127.0.0.1", "", true},
		{"https://evil.example", "", false},
		{"http://10.0.0.5:3030", "10.0.0.5", true},
		{"vscode-file://vscode-app", "", true},
	}
	for _, tt := range tests {
		if got := OriginAllowed(tt.origin, tt.bind); got != tt.ok {
			t.Fatalf("OriginAllowed(%q, %q)=%v want %v", tt.origin, tt.bind, got, tt.ok)
		}
	}
}
