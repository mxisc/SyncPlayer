package device

import (
	"syncplayer/internal/script"
	"testing"
	"time"
)

func TestStopCancelsHandyEndpointSchedule(t *testing.T) {
	c := &Controller{handyClock: &script.MotionClock{}, handyNext: time.Now().Add(time.Millisecond), active: true}
	if err := c.stopStroke(); err != nil {
		t.Fatal(err)
	}
	if c.handyClock != nil || !c.handyNext.IsZero() || c.active {
		t.Fatal("stopped controller retained a scheduled motion")
	}
}

func TestProgressCorrectionDoesNotInterruptHold(t *testing.T) {
	start := time.Now()
	actions := []script.Action{{At: 0, Pos: 50}, {At: 318270, Pos: 50}, {At: 320671, Pos: 100}}
	clock := &script.MotionClock{Actions: actions, Rate: 1, Anchor: start}
	_, _, deadline, _ := clock.Next(start)
	c := &Controller{queue: make(chan request), handy: true, active: true, handyClock: clock, handyNext: deadline, handyDuration: 500 * time.Millisecond, handyTarget: .5}
	go func() { r := <-c.queue; r.done <- r.fn() }()
	if err := c.FollowScript(actions, 1000, 1); err != nil {
		t.Fatal(err)
	}
	if c.handyClock != clock || !c.handyNext.Equal(deadline) || !clock.Hold || c.handyDuration != 500*time.Millisecond || c.handyTarget != .5 {
		t.Fatal("clock correction cancelled or replaced the issued hold")
	}
}

func TestHandyUsesSerialIdentityInsteadOfChangingAdvertisementData(t *testing.T) {
	if got := handySerialIdentity("OHD_hw4_507c2cc4761c"); got != "handy:ohd_hw4_507c2cc4761c" {
		t.Fatal("missing serial identity")
	}
	for _, name := range []string{"The Handy", "FN010-RX", "OHD_hw1_507c2cc4761c", "OHD_hw4_not-a-serial"} {
		if handySerialIdentity(name) != "" {
			t.Fatal("non-unique name accepted as hardware serial")
		}
	}
}
