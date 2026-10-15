package device

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestHandyWireFormats(t *testing.T) {
	c := &Controller{handyV4: true}
	packet := c.handyPacket(744, handyV4PositionPayload(.75, 100, false))
	fields, e := wireFields(packet)
	if e != nil || len(fields) != 2 || fields[0].value != 1 {
		t.Fatal("invalid v4 envelope", e)
	}
	req, e := wireFields(fields[1].data)
	if e != nil || req[0].number != 2 || req[0].value != 1 || req[1].number != 744 {
		t.Fatal("invalid v4 request", e)
	}
	params, e := wireFields(req[1].data)
	if e != nil || len(params) != 3 || params[1].number != 2 || params[1].value != 100 || params[2].number != 3 || params[2].value != 0 {
		t.Fatal("HDSP streaming changed duration or enabled stop-on-target", e)
	}
	raw := pf(1, .75)
	if math.Float32frombits(binary.LittleEndian.Uint32(raw[1:])) != .75 {
		t.Fatal("position float encoding")
	}
	ok := append(pv(1, 3), pb(4, pv(1, 1))...)
	if e := checkHandyReply(ok, 1); e != nil {
		t.Fatal(e)
	}
	denied := append(pv(1, 3), pb(4, append(pv(1, 1), pb(2, pv(1, 4000))...))...)
	if checkHandyReply(denied, 1) == nil {
		t.Fatal("device error ignored")
	}
	if checkHandyReply(ok, 2) == nil {
		t.Fatal("wrong response ID accepted")
	}
	for _, bad := range [][]byte{{0x12, 0xff}, {0}, {0xff}} {
		if _, e := wireFields(bad); e == nil {
			t.Fatal("malformed response accepted")
		}
	}
	c.handyV4 = false
	legacy := c.handyPacket(403, pb(3, append(pv(2, 100), pd(3, .75)...)))
	outer, e := wireFields(legacy)
	if e != nil || outer[0].number != 1 {
		t.Fatal("invalid handyplug payload")
	}
	message, _ := wireFields(outer[0].data)
	if message[0].number != 403 {
		t.Fatal("invalid handyplug command")
	}
}

func TestStationaryHandyTargetStopsOnArrival(t *testing.T) {
	params, err := wireFields(handyV4PositionPayload(.5, 500, true))
	if err != nil || len(params) != 3 || params[2].number != 3 || params[2].value != 1 {
		t.Fatal("stationary target did not request hold", err)
	}
}
func TestSupportedHandyModels(t *testing.T) {
	for _, name := range []string{"The Handy", "OHD_hw0_0904", "OHD_hw4_1234"} {
		if !supportedHandy(name) {
			t.Fatal("missing Handy model")
		}
	}
	for _, name := range []string{"OHD_hw1_1234", "random", "FN010-RX"} {
		if supportedHandy(name) {
			t.Fatal("unrelated device classified as Handy")
		}
	}
}
