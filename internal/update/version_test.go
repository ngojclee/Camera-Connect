package update

import "testing"

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in   string
		want [4]int
		ok   bool
	}{
		{in: "v2.0.0.0", want: [4]int{2, 0, 0, 0}, ok: true},
		{in: "2.1.3", want: [4]int{2, 1, 3, 0}, ok: true},
		{in: "1.2.3.4-beta1", want: [4]int{1, 2, 3, 4}, ok: true},
		{in: "V10.20.30+meta", want: [4]int{10, 20, 30, 0}, ok: true},
		{in: "", ok: false},
		{in: "abc", ok: false},
		{in: "1.2.3.4.5", ok: false},
	}

	for _, tc := range tests {
		got, err := ParseVersion(tc.in)
		if tc.ok && err != nil {
			t.Fatalf("ParseVersion(%q) unexpected error: %v", tc.in, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("ParseVersion(%q) expected error", tc.in)
		}
		if !tc.ok {
			continue
		}
		if got != tc.want {
			t.Fatalf("ParseVersion(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{a: "1.0.0", b: "1.0.0.0", want: 0},
		{a: "1.0.0.1", b: "1.0.0.0", want: 1},
		{a: "v2.0.0", b: "2.1.0", want: -1},
		{a: "3.0.0", b: "2.9.9.9", want: 1},
	}

	for _, tc := range cases {
		got, err := CompareVersions(tc.a, tc.b)
		if err != nil {
			t.Fatalf("CompareVersions(%q,%q) error: %v", tc.a, tc.b, err)
		}
		if got != tc.want {
			t.Fatalf("CompareVersions(%q,%q)=%d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
