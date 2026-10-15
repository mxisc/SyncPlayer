package script

import (
	"math"
	"sort"
	"time"
)

// MotionClock schedules original endpoints against one monotonic playback anchor.
// Long straight segments are split only to bound unattended movement, never across a turn.
type MotionClock struct {
	Actions []Action
	Rate    float64
	MS      int64
	Anchor  time.Time
	next    int
	started bool
	Hold    bool
	endMS   *float64
}

// Finish retains pending endpoints up to the playback end, not later script content.
func (c *MotionClock) Finish(now time.Time) {
	if c.endMS == nil {
		ms := c.PositionMS(now)
		c.endMS = &ms
	}
}

func (c *MotionClock) PositionMS(now time.Time) float64 {
	return float64(c.MS) + now.Sub(c.Anchor).Seconds()*1000*c.Rate
}

func (c *MotionClock) Matches(actions []Action, ms int64, rate float64, now time.Time) bool {
	return len(actions) > 0 && len(actions) == len(c.Actions) && &actions[0] == &c.Actions[0] && rate == c.Rate && math.Abs(c.PositionMS(now)-float64(ms)) <= 150
}

func (c *MotionClock) Next(now time.Time) (pos float64, duration time.Duration, deadline time.Time, ok bool) {
	if c.Rate <= 0 || math.IsNaN(c.Rate) || math.IsInf(c.Rate, 0) {
		return
	}
	if !c.started {
		c.next = sort.Search(len(c.Actions), func(i int) bool { return c.Actions[i].At > c.MS })
		c.started = true
	}
	i := c.next
	if i == len(c.Actions) {
		return
	}
	next := c.Actions[i]
	if c.endMS != nil && float64(next.At) > *c.endMS {
		return
	}
	deadline = c.Anchor.Add(time.Duration(math.Ceil((float64(next.At) - float64(c.MS)) / c.Rate * float64(time.Millisecond))))
	duration = deadline.Sub(now)
	pos = float64(next.Pos) / 100
	c.Hold = i == 0 || c.Actions[i-1].Pos == next.Pos
	if c.Hold {
		// Reach a stationary target once, then wait until the plateau ends.
		// The command duration bounds initial positioning, while deadline keeps
		// the whole hold interval free of repeated motion commands.
		duration = max(time.Millisecond, min(500*time.Millisecond, duration))
		c.next++
	} else if duration > 500*time.Millisecond {
		duration = 500 * time.Millisecond
		deadline = now.Add(duration)
		if i > 0 {
			prev := c.Actions[i-1]
			target := c.PositionMS(deadline)
			fraction := (target - float64(prev.At)) / float64(next.At-prev.At)
			pos = (float64(prev.Pos) + float64(next.Pos-prev.Pos)*fraction) / 100
		}
	} else {
		// A late endpoint remains queued. Submit it before advancing to the next.
		c.next++
		duration = max(time.Millisecond, duration)
	}
	pos = math.Max(0, math.Min(1, pos))
	ok = duration > 0
	return
}
