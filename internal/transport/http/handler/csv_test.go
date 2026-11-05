package handler

import "testing"

func TestSanitizeCell(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"plain", "plain"},
		{"has = inside", "has = inside"},
		{"a-b", "a-b"},
		{"=1+1", "'=1+1"},
		{"+1", "'+1"},
		{"-1", "'-1"},
		{"@x", "'@x"},
		{"\t=x", "'\t=x"},
		{"\r=x", "'\r=x"},
		{"'=already", "'=already"}, // a leading quote is not a formula trigger
		{"é=", "é="},
	}
	for _, tt := range tests {
		if got := sanitizeCell(tt.in); got != tt.want {
			t.Errorf("sanitizeCell(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
