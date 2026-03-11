package utils

import "testing"

func TestStripANSISequencesRemovesColorCodes(t *testing.T) {
	in := "2026/03/11 - 18:17:17 | host |\x1b[97;42m 200 \x1b[0m|\x1b[97;44m GET \x1b[0m /"
	out := StripANSISequences(in)

	expected := "2026/03/11 - 18:17:17 | host | 200 | GET  /"
	if out != expected {
		t.Fatalf("unexpected sanitized output: got %q want %q", out, expected)
	}
}

func TestStripANSISequencesWithoutEscapeReturnsInput(t *testing.T) {
	in := "plain monochrome line"
	out := StripANSISequences(in)

	if out != in {
		t.Fatalf("expected unchanged input, got %q", out)
	}
}
