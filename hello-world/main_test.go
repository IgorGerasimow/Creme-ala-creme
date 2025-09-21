package main

import "testing"

func Test_parseBoolEnv(t *testing.T) {
	tests := []struct{
		in string
		def bool
		want bool
	}{
		{"1", false, true},
		{"true", false, true},
		{"on", false, true},
		{"0", true, false},
		{"false", true, false},
		{"", true, true},
		{"", false, false},
	}
	for _, tt := range tests {
		if got := parseBoolEnv(tt.in, tt.def); got != tt.want {
			t.Fatalf("parseBoolEnv(%q,%v)=%v want %v", tt.in, tt.def, got, tt.want)
		}
	}
}
