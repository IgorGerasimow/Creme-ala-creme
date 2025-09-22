package main

import (
	"os"
	"testing"
)

func TestGetBoolEnv(t *testing.T) {
	const envVar = "TEST_BOOL_FLAG"

	tests := []struct {
		name   string
		set    bool
		value  string
		def    bool
		expect bool
	}{
		{name: "truthy numeric", set: true, value: "1", def: false, expect: true},
		{name: "truthy word", set: true, value: "true", def: false, expect: true},
		{name: "truthy on", set: true, value: "on", def: false, expect: true},
		{name: "falsy numeric", set: true, value: "0", def: true, expect: false},
		{name: "falsy word", set: true, value: "false", def: true, expect: false},
		{name: "unset defaults true", set: false, def: true, expect: true},
		{name: "unset defaults false", set: false, def: false, expect: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(envVar, tt.value)
			} else {
				os.Unsetenv(envVar)
			}

			if got := getBoolEnv(envVar, tt.def); got != tt.expect {
				t.Fatalf("getBoolEnv(%q,%v)=%v want %v", envVar, tt.def, got, tt.expect)
			}
		})
	}
}
