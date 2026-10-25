package utils

import "testing"

func TestDNSTXTName(t *testing.T) {
	cases := map[string]string{
		"example.com":  "_sish.example.com.",
		"example.com.": "_sish.example.com.",
		"a.b.example":  "_sish.a.b.example.",
	}

	for addr, want := range cases {
		if got := dnsTXTName(addr); got != want {
			t.Errorf("dnsTXTName(%q) = %q, want %q", addr, got, want)
		}
	}
}
