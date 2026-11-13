package version

import "testing"

// Sin commit inyectado (go build pelado, o sin git) se muestra "dev".
func TestCommitText(t *testing.T) {
	saved := Commit
	defer func() { Commit = saved }()
	for _, c := range []struct{ commit, want string }{
		{"", "dev"},
		{"  ", "dev"},
		{"c1d2462", "c1d2462"},
	} {
		Commit = c.commit
		if got := CommitText(); got != c.want {
			t.Errorf("Commit %q: %q, quería %q", c.commit, got, c.want)
		}
	}
}
