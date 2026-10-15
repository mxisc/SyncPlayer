package deovr

import (
	"encoding/binary"
	"encoding/json"
	"net"
	"syncplayer/internal/player"
	"testing"
	"time"
)

// The TCP stream can split the length prefix and JSON at any byte boundary.
// This exercises the real reader rather than calling update directly.
func TestClassicProgressStream(t *testing.T) {
	left, right := net.Pipe()
	c := New(nil)
	c.conn = left
	c.state.Connected = true
	defer c.Disconnect()
	defer right.Close()
	go c.read(left)
	send := func(payload any, wantTime float64, paused bool) {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		frame := make([]byte, 4+len(body))
		binary.LittleEndian.PutUint32(frame, uint32(len(body)))
		copy(frame[4:], body)
		for _, part := range [][]byte{frame[:2], frame[2:7], frame[7:]} {
			if _, err := right.Write(part); err != nil {
				t.Fatal(err)
			}
		}
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			c.mu.Lock()
			s := c.state
			c.mu.Unlock()
			if s.Loaded && s.Time == wantTime && s.Paused == paused {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("Classic progress packet not reflected in playback state")
	}
	send(map[string]any{"path": "https://example.invalid/video.mp4", "duration": 120, "currentTime": 10.5, "playbackSpeed": 1, "playerState": 0}, 10.5, false)
	if s := c.Snapshot(); s.Source == "" || s.Time < 10.5 || s.Time > 11 {
		t.Fatal("playing clock not anchored to reported time")
	}
	send(map[string]any{"currentTime": 11, "playerState": 1}, 11, true)
	if s := c.Snapshot(); s.Time != 11 || !s.Paused {
		t.Fatal("paused clock advanced")
	}
	send(map[string]any{"currentTime": 75, "playerState": 1}, 75, true)
	send(map[string]any{"currentTime": 75.25, "playbackSpeed": 1.5, "playerState": 0}, 75.25, false)
	if s := c.Snapshot(); s.Rate != 1.5 || s.Paused {
		t.Fatal("resume/rate state not applied")
	}
	c.mu.Lock()
	c.stamp = time.Now().Add(-staleAfter - time.Second)
	c.mu.Unlock()
	if s := c.Snapshot(); s.Loaded || !s.Paused {
		t.Fatal("stale progress remained usable for device synchronization")
	}
}

func TestEmptyPacketIsOnlyHeartbeat(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	c := New(nil)
	c.conn = left
	c.path = "test.mp4"
	c.stamp = time.Now()
	c.state = State{State: player.State{Loaded: true, Paused: true, Time: 12, Duration: 90}, Connected: true, Source: "source"}
	c.update(left, nil)
	if c.path != "test.mp4" || !c.state.Loaded || c.state.Time != 12 || c.state.Source != "source" {
		t.Fatal("heartbeat cleared playback")
	}
	if c.lastPacket.IsZero() {
		t.Fatal("heartbeat timestamp not updated")
	}
	empty := ""
	c.update(left, &packet{Path: &empty})
	if c.state.Loaded || c.state.Source != "" {
		t.Fatal("explicit empty path failed to close video")
	}
}
