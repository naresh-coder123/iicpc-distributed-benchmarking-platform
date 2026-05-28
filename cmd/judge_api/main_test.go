package main

import "testing"

func TestIsValidID(t *testing.T) {
	valid := []string{
		"team-alpha",
		"team_alpha",
		"TeamAlpha",
		"t",
		"a1b2c3",
		"UPPER-lower_123",
	}
	for _, id := range valid {
		if !isValidID(id) {
			t.Errorf("expected %q to be valid", id)
		}
	}

	invalid := []string{
		"",
		"has space",
		"has.dot",
		"has@symbol",
		"has/slash",
		"has:colon",
		// 65 chars — over the limit
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1",
	}
	for _, id := range invalid {
		if isValidID(id) {
			t.Errorf("expected %q to be invalid", id)
		}
	}
}
