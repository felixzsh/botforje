package whatsapp

import "testing"

func TestNumber(t *testing.T) {
	cases := map[string]string{
		"5215500000000:65@s.whatsapp.net": "5215500000000",
		"5215500000000@s.whatsapp.net":    "5215500000000",
		"5215500000000:65":                "5215500000000",
		"5215500000000":                   "5215500000000",
	}
	for in, want := range cases {
		if got := Number(in); got != want {
			t.Fatalf("Number(%q) = %q, want %q", in, got, want)
		}
	}
}
