package script

import (
	"math"
	"testing"
	"time"
)

func TestMotionKeepsEverySub100msTurn(t *testing.T) {
	actions := []Action{{0, 0}, {20, 100}, {40, 0}, {60, 100}, {80, 0}, {100, 100}}
	for _, rate := range []float64{1, 2, .5} {
		start := time.Unix(100, 0)
		clock := MotionClock{Actions: actions, MS: 0, Rate: rate, Anchor: start}
		now := start
		for _, action := range actions[1:] {
			pos, duration, deadline, ok := clock.Next(now)
			want := start.Add(time.Duration(float64(action.At) / rate * float64(time.Millisecond)))
			if !ok || pos != float64(action.Pos)/100 || !deadline.Equal(want) || duration != want.Sub(now) {
				t.Fatalf("lost endpoint at %d ms (rate %v): pos=%v duration=%v deadline=%v", action.At, rate, pos, duration, deadline.Sub(start))
			}
			now = deadline
		}
		if _, _, _, ok := clock.Next(now); ok {
			t.Fatal("finished script scheduled another action")
		}
	}
}

func TestMotionSplitsLongSegmentsWithoutSkippingTurns(t *testing.T) {
	start := time.Unix(100, 0)
	clock := MotionClock{Actions: []Action{{0, 0}, {1200, 100}, {1250, 0}}, Rate: 1, Anchor: start}
	now := start
	for _, want := range []struct {
		ms  int
		pos float64
	}{{500, 5.0 / 12}, {1000, 10.0 / 12}, {1200, 1}, {1250, 0}} {
		pos, duration, deadline, ok := clock.Next(now)
		if !ok || math.Abs(pos-want.pos) > 1e-9 || deadline.Sub(start) != time.Duration(want.ms)*time.Millisecond || duration > 500*time.Millisecond {
			t.Fatalf("incorrect long-segment scheduling: %v %v %v", pos, duration, deadline.Sub(start))
		}
		now = deadline
	}
}

func TestMotionDelayPreservesExpiredCommandsInOrder(t *testing.T) {
	start := time.Unix(100, 0)
	actions := []Action{{0, 0}, {20, 100}, {40, 0}, {60, 100}, {80, 0}, {200, 100}}
	clock := MotionClock{Actions: actions, Rate: 1, Anchor: start}
	for _, action := range actions[1:] {
		pos, duration, deadline, ok := clock.Next(start.Add(95 * time.Millisecond))
		want := time.Millisecond
		if action.At > 95 {
			want = time.Duration(action.At-95) * time.Millisecond
		}
		if !ok || pos != float64(action.Pos)/100 || duration != want || deadline.Sub(start) != time.Duration(action.At)*time.Millisecond {
			t.Fatal("delayed scheduler dropped or reordered an endpoint")
		}
	}
	if !clock.Matches(actions, 100, 1, start.Add(100*time.Millisecond)) {
		t.Fatal("ordinary progress heartbeat interrupted endpoint schedule")
	}
	if clock.Matches(actions, 500, 1, start.Add(100*time.Millisecond)) {
		t.Fatal("seek did not invalidate anchor")
	}
	if clock.Matches(actions, 100, 2, start.Add(100*time.Millisecond)) {
		t.Fatal("speed change did not invalidate anchor")
	}
	replacement := append([]Action(nil), actions...)
	if clock.Matches(replacement, 100, 1, start.Add(100*time.Millisecond)) {
		t.Fatal("new script did not invalidate anchor")
	}
}

func TestMotionQueuesOneHundredOneMillisecondPoints(t *testing.T) {
	start := time.Unix(100, 0)
	actions := []Action{{At: 0, Pos: 0}}
	for i := 1; i <= 100; i++ {
		actions = append(actions, Action{At: int64(i), Pos: i % 2 * 100})
	}
	clock := MotionClock{Actions: actions, Rate: 1, Anchor: start}
	// Simulate slow submissions: every original point must still be returned once.
	for i := 1; i <= 100; i++ {
		pos, _, deadline, ok := clock.Next(start.Add(time.Duration(i*5) * time.Millisecond))
		if !ok || pos != float64(actions[i].Pos)/100 || deadline.Sub(start) != time.Duration(i)*time.Millisecond {
			t.Fatalf("missing endpoint %d", i)
		}
	}
	if _, _, _, ok := clock.Next(start.Add(time.Second)); ok {
		t.Fatal("endpoint duplicated")
	}
}

func TestLongMidpointPlateauHoldsUntilFirstRealMovement(t *testing.T) {
	start := time.Unix(100, 0)
	clock := MotionClock{Actions: []Action{{0, 50}, {318270, 50}, {320671, 100}}, Rate: 1, MS: 50, Anchor: start}
	pos, duration, deadline, ok := clock.Next(start)
	if !ok || pos != .5 || !clock.Hold || duration != 500*time.Millisecond || deadline.Sub(start) != 318220*time.Millisecond {
		t.Fatal("initial flat section was scheduled as continuous motion")
	}
	pos, duration, _, ok = clock.Next(deadline)
	if !ok || clock.Hold || pos <= .5 || pos >= 1 || duration != 500*time.Millisecond {
		t.Fatal("movement did not resume after the plateau")
	}
}

func TestPlaybackEndDrainsOverduePointsButNotFutureContent(t *testing.T) {
	start := time.Unix(100, 0)
	clock := MotionClock{Actions: []Action{{0, 0}, {20, 100}, {40, 0}, {60, 100}, {80, 0}, {200, 100}}, Rate: 1, Anchor: start}
	clock.Finish(start.Add(95 * time.Millisecond))
	clock.Finish(start.Add(time.Second)) // Repeated EOF ticks cannot extend playback.
	for _, want := range []float64{1, 0, 1, 0} {
		pos, _, _, ok := clock.Next(start.Add(time.Second))
		if !ok || pos != want {
			t.Fatal("EOF discarded a pending endpoint")
		}
	}
	if _, _, _, ok := clock.Next(start.Add(time.Second)); ok {
		t.Fatal("EOF emitted future script content")
	}
}
