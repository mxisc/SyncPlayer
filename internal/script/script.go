package script

import (
	"encoding/json"
	"errors"
	"sort"
)

type Action struct {
	At  int64 `json:"at"`
	Pos int   `json:"pos"`
}
type Script struct {
	Actions []Action `json:"actions"`
	Cycles  []Cycle  `json:"-"`
}

type Cycle struct {
	Start int64
	End   int64
}

// Level 4 duplicates level 1 and is excluded from automatic selection.
// Levels 5–8 are rough measurements; levels 9–10 are extrapolated estimates.
var CycleMS = [...]float64{0, 10000.0 / 14.5, 10000.0 / 17.5, 10000.0 / 21.5, 10000.0 / 14.5, 10000.0 / 22, 10000.0 / 26.5, 10000.0 / 28.5, 10000.0 / 30.5, 10000.0 / 32.5, 10000.0 / 34.5}

// Each pair of direction reversals returns to the starting side.
func cycles(points []Action) []Cycle {
	turns := []int64{}
	direction := 0
	for i := 1; i < len(points); i++ {
		delta := points[i].Pos - points[i-1].Pos
		if delta == 0 {
			continue
		}
		next := 1
		if delta < 0 {
			next = -1
		}
		if direction == 0 || next != direction {
			turns = append(turns, points[i-1].At)
		}
		direction = next
	}
	if direction != 0 {
		turns = append(turns, points[len(points)-1].At)
	}
	result := []Cycle{}
	for i := 0; i+2 < len(turns); i += 2 {
		if turns[i+2] > turns[i] {
			result = append(result, Cycle{turns[i], turns[i+2]})
		}
	}
	return result
}

func (s Script) CycleAt(ms int64) (int, Cycle, bool) {
	i := sort.Search(len(s.Cycles), func(i int) bool { return s.Cycles[i].Start > ms }) - 1
	if i < 0 || ms >= s.Cycles[i].End {
		return -1, Cycle{}, false
	}
	return i, s.Cycles[i], true
}

func SelectLevel(span int64, cap int) int {
	if cap > 10 {
		cap = 10
	}
	if cap <= 0 {
		return 0
	}
	for level := 1; level <= cap; level++ {
		if level == 4 {
			continue
		}
		if CycleMS[level] <= float64(span) {
			return level
		}
	}
	if cap == 4 {
		return 3
	}
	return cap
}

func Parse(data []byte) (Script, error) {
	var s Script
	if e := json.Unmarshal(data, &s); e != nil {
		return s, e
	}
	if len(s.Actions) == 0 {
		return s, errors.New("脚本没有 actions")
	}
	for _, a := range s.Actions {
		if a.At < 0 || a.Pos < 0 || a.Pos > 100 {
			return s, errors.New("脚本时间或位置超出范围")
		}
	}
	sort.SliceStable(s.Actions, func(i, j int) bool { return s.Actions[i].At < s.Actions[j].At })
	s.Cycles = cycles(s.Actions)
	return s, nil
}
func (s Script) Level(ms int64) int {
	i := sort.Search(len(s.Actions), func(i int) bool { return s.Actions[i].At > ms }) - 1
	if i < 0 || ms > s.Actions[len(s.Actions)-1].At {
		return 0
	}
	return (s.Actions[i].Pos + 9) / 10
}
