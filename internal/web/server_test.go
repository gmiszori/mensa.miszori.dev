package web

import "testing"

func TestTimeLeft(t *testing.T) {
	for _, c := range []struct{ from, to, want string }{
		{"12:10", "13:30", "1h 20"},
		{"12:45", "13:30", "45 min"},
		{"11:00", "14:00", "3h 00"},
		{"13:29", "13:30", "1 min"},
	} {
		if got := timeLeft(c.from, c.to); got != c.want {
			t.Errorf("timeLeft(%q, %q) = %q, want %q", c.from, c.to, got, c.want)
		}
	}
}
