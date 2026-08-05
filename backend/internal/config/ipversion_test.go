package config

import "testing"

func TestResolveIPFamily(t *testing.T) {
	cases := []struct {
		in      string
		wantV4  bool
		wantV6  bool
		wantErr bool
	}{
		{"", true, false, false},
		{"v4", true, false, false},
		{"V4", true, false, false},
		{"v6", false, true, false},
		{"v4v6", true, true, false},
		{"dual", true, true, false},
		{"bogus", false, false, true},
	}

	for _, c := range cases {
		v4, v6, err := ResolveIPFamily(c.in)
		if (err != nil) != c.wantErr {
			t.Fatalf("ResolveIPFamily(%q) err=%v wantErr=%v", c.in, err, c.wantErr)
		}
		if err != nil {
			continue
		}
		if v4 != c.wantV4 || v6 != c.wantV6 {
			t.Errorf("ResolveIPFamily(%q)=(%v,%v) want (%v,%v)", c.in, v4, v6, c.wantV4, c.wantV6)
		}
	}
}
