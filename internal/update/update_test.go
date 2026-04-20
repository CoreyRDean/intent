package update

import "testing"

func TestSemverLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.1.0", "0.2.0", true},
		{"1.0.0", "1.0.1", true},
		{"1.0.0", "1.0.0", false},
		{"1.0.0-1", "1.0.0", true},       // pre-release < release
		{"1.0.0-100", "1.0.0-200", true}, // numeric pre sorts numerically
		{"0.0.1-1745090000", "0.0.1-1745176400", true},
	}
	for _, c := range cases {
		if got := semverLess(c.a, c.b); got != c.want {
			t.Errorf("semverLess(%q,%q)=%v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestIsNightlyTag(t *testing.T) {
	cases := map[string]bool{
		"v0.1.0":            false,
		"v0.1.0-1745090000": true,
		"v1.2.3-rc1":        false,
	}
	for tag, want := range cases {
		if got := isNightlyTag(tag); got != want {
			t.Errorf("isNightlyTag(%q)=%v want %v", tag, got, want)
		}
	}
}
