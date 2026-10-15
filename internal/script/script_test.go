package script

import "testing"

func TestTimeline(t *testing.T) {
	s, e := Parse([]byte(`{"actions":[{"at":1000,"pos":100},{"at":0,"pos":20},{"at":2000,"pos":0}]}`))
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range []struct {
		ms    int64
		level int
	}{{-1, 0}, {0, 2}, {999, 2}, {1000, 10}, {500, 2}, {2000, 0}} {
		if got := s.Level(c.ms); got != c.level {
			t.Fatalf("at %d got %d want %d", c.ms, got, c.level)
		}
	}
}
func TestInvalid(t *testing.T) {
	for _, data := range []string{`{}`, `{"actions":[{"at":-1,"pos":0}]}`, `{"actions":[{"at":0,"pos":101}]}`} {
		if _, e := Parse([]byte(data)); e == nil {
			t.Fatal("accepted", data)
		}
	}
}
