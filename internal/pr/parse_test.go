package pr

import "testing"

func TestParseRef(t *testing.T) {
	cases := []struct {
		in        string
		want      Ref
		wantError bool
	}{
		{"1234", Ref{Number: 1234}, false},
		{"foo/bar#89", Ref{Owner: "foo", Repo: "bar", Number: 89}, false},
		{"https://github.com/foo/bar/pull/89", Ref{Owner: "foo", Repo: "bar", Number: 89}, false},
		{"https://github.com/foo/bar/pull/89/", Ref{Owner: "foo", Repo: "bar", Number: 89}, false},
		{"https://github.com/foo/bar/pull/89/files", Ref{Owner: "foo", Repo: "bar", Number: 89}, false},
		{"", Ref{}, true},
		{"0", Ref{}, true},
		{"-1", Ref{}, true},
		{"foo/bar", Ref{}, true},
		{"https://gitlab.com/foo/bar/pull/89", Ref{}, true},
		{"https://github.com/foo/bar/issues/89", Ref{}, true},
		{"foo bar#89", Ref{}, true},
	}
	for _, c := range cases {
		got, err := ParseRef(c.in)
		if c.wantError {
			if err == nil {
				t.Errorf("ParseRef(%q): want error, got %+v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseRef(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseRef(%q): got %+v want %+v", c.in, got, c.want)
		}
	}
}
