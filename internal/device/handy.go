package device

import (
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"syncplayer/internal/diagnostics"
	"syncplayer/internal/script"
	"time"
	"tinygo.org/x/bluetooth"
)

// Wire fields follow Sweet Tech's handyplug.proto (FW3) and hdy_rpc (FW4).
const handyService = "1775244d-6b43-439b-877c-060f2d9bed07"
const handyV4Service = "77834d26-40f7-11ee-be56-0242ac120002"

func supportedHandy(name string) bool {
	n := strings.ToLower(name)
	return n == "the handy" || strings.HasPrefix(n, "ohd_hw0_") || strings.HasPrefix(n, "ohd_hw2_") || strings.HasPrefix(n, "ohd_hw3_") || strings.HasPrefix(n, "ohd_hw4_")
}
func pv(field int, v uint64) []byte {
	b := binary.AppendUvarint(nil, uint64(field<<3))
	return binary.AppendUvarint(b, v)
}
func pb(field int, v []byte) []byte {
	b := binary.AppendUvarint(nil, uint64(field<<3|2))
	b = binary.AppendUvarint(b, uint64(len(v)))
	return append(b, v...)
}
func pf(field int, v float32) []byte {
	b := binary.AppendUvarint(nil, uint64(field<<3|5))
	return binary.LittleEndian.AppendUint32(b, math.Float32bits(v))
}
func pd(field int, v float64) []byte {
	b := binary.AppendUvarint(nil, uint64(field<<3|1))
	return binary.LittleEndian.AppendUint64(b, math.Float64bits(v))
}
func (c *Controller) handyPacket(field int, params []byte) []byte {
	c.handySeq++
	if c.handyV4 {
		req := append(pv(2, c.handySeq), pb(field, params)...)
		return append(pv(1, 1), pb(2, req)...)
	}
	inner := append(pv(1, c.handySeq), params...)
	return pb(1, pb(field, inner))
}
func (c *Controller) handyWrite(data []byte) error {
	if c.characteristic == nil {
		return errors.New("Handy 蓝牙未连接")
	}
	var n int
	var e error
	if c.characteristic.Properties()&0x04 != 0 {
		n, e = c.characteristic.WriteWithoutResponse(data)
	} else {
		n, e = c.characteristic.Write(data)
	}
	if e != nil {
		return errors.New("Handy 蓝牙写入失败")
	}
	if n != len(data) {
		return errors.New("Handy 蓝牙指令未完整写入")
	}
	return nil
}

type wireField struct {
	number int
	value  uint64
	data   []byte
}

func wireFields(data []byte) ([]wireField, error) {
	var result []wireField
	for len(data) > 0 {
		tag, n := binary.Uvarint(data)
		if n <= 0 {
			return nil, errors.New("Handy 蓝牙响应格式错误")
		}
		data = data[n:]
		f := wireField{number: int(tag >> 3)}
		switch tag & 7 {
		case 0:
			v, n := binary.Uvarint(data)
			if n <= 0 {
				return nil, errors.New("Handy 蓝牙响应格式错误")
			}
			f.value = v
			data = data[n:]
		case 2:
			size, n := binary.Uvarint(data)
			if n <= 0 || size > uint64(len(data)-n) {
				return nil, errors.New("Handy 蓝牙响应格式错误")
			}
			data = data[n:]
			f.data = data[:int(size)]
			data = data[int(size):]
		case 1:
			if len(data) < 8 {
				return nil, errors.New("Handy 蓝牙响应格式错误")
			}
			data = data[8:]
		case 5:
			if len(data) < 4 {
				return nil, errors.New("Handy 蓝牙响应格式错误")
			}
			data = data[4:]
		default:
			return nil, errors.New("Handy 蓝牙响应格式错误")
		}
		result = append(result, f)
	}
	return result, nil
}
func checkHandyReply(data []byte, expected uint64) error {
	fields, e := wireFields(data)
	if e != nil {
		return e
	}
	response := false
	correctID := false
	for _, f := range fields {
		if f.number == 1 && f.value != 3 {
			return errors.New("Handy 蓝牙返回了非响应消息")
		}
		if f.number == 4 {
			response = true
			inner, e := wireFields(f.data)
			if e != nil {
				return e
			}
			for _, v := range inner {
				if v.number == 1 && v.value == expected {
					correctID = true
				}
				if v.number == 2 && len(v.data) > 0 {
					return errors.New("Handy 拒绝了蓝牙指令")
				}
			}
		}
	}
	if !response || !correctID {
		return errors.New("Handy 蓝牙响应与指令不匹配")
	}
	return nil
}
func (c *Controller) connectHandy(s bluetooth.DeviceService) error {
	c.handy = true
	c.handyV4 = s.UUID().String() == handyV4Service
	c.handySeq = 0
	c.handyTarget = 0.5
	c.handyFrom = 0.5
	c.handyReplies = make(chan []byte, 16)
	chars, e := s.DiscoverCharacteristics(nil)
	if e != nil {
		return e
	}
	var session *bluetooth.DeviceCharacteristic
	for i := range chars {
		ch := &chars[i]
		id := ch.UUID().String()
		if id == "1775ff55-6b43-439b-877c-060f2d9bed07" || id == "77835032-40f7-11ee-be56-0242ac120002" {
			c.characteristic = ch
		}
		if id == "1775ff51-6b43-439b-877c-060f2d9bed07" {
			session = ch
		}
		if id == "77835410-40f7-11ee-be56-0242ac120002" {
			replies := c.handyReplies
			if e := ch.EnableNotifications(func(data []byte) {
				select {
				case replies <- append([]byte(nil), data...):
				default:
				}
			}); e != nil {
				return e
			}
		}
	}
	if c.characteristic == nil {
		return errors.New("未发现 Handy 控制特征")
	}
	if c.handyV4 {
		if e := c.handyWrite(c.handyPacket(710, nil)); e != nil {
			return e
		}
		select {
		case reply := <-c.handyReplies:
			if e := checkHandyReply(reply, c.handySeq); e != nil {
				return e
			}
		case <-time.After(4 * time.Second):
			return errors.New("Handy 蓝牙握手超时")
		}
	} else if session != nil {
		if _, e := session.Write(pb(10, pb(20, nil))); e != nil {
			return e
		}
		buf := make([]byte, 128)
		if _, e := session.Read(buf); e != nil {
			return e
		}
		if e := c.handyWrite(c.handyPacket(200, nil)); e != nil {
			return e
		}
		if _, e := c.characteristic.Read(buf); e != nil {
			return e
		}
	}
	c.handyPing = time.Now()
	return nil
}
func (c *Controller) handyKeepalive() error {
	field := 102
	if c.handyV4 {
		field = 710
	}
	c.handyPing = time.Now()
	return c.handyWrite(c.handyPacket(field, nil))
}
func (c *Controller) handyPosition() float64 {
	if c.handyDuration <= 0 {
		return c.handyTarget
	}
	f := math.Min(1, float64(time.Since(c.handyStart))/float64(c.handyDuration))
	return c.handyFrom + (c.handyTarget-c.handyFrom)*f
}

func handyV4PositionPayload(pos float32, ms uint64, hold bool) []byte {
	// Moving segments avoid the firmware's asymmetric stop-on-target behavior.
	// Stationary targets need stop-on-target; explicit pause uses RPC 746.
	stop := uint64(0)
	if hold {
		stop = 1
	}
	return append(append(pf(1, pos), pv(2, ms)...), pv(3, stop)...)
}

func (c *Controller) handyMove(pos float64, duration time.Duration, hold bool) error {
	if duration <= 0 || duration > time.Second || math.IsNaN(pos) || math.IsInf(pos, 0) {
		return errors.New("Handy 位置参数无效")
	}
	if c.handyV4 {
		for {
			select {
			case reply := <-c.handyReplies:
				fields, e := wireFields(reply)
				if e != nil {
					return e
				}
				for _, f := range fields {
					if f.number == 4 {
						inner, e := wireFields(f.data)
						if e != nil {
							return e
						}
						for _, v := range inner {
							if v.number == 2 && len(v.data) > 0 {
								return errors.New("Handy 拒绝了蓝牙控制指令")
							}
						}
					}
				}
			default:
				goto drained
			}
		}
	}
drained:
	pos = math.Max(0, math.Min(1, pos))
	ms := uint64(max(1, duration.Milliseconds()))
	var packet []byte
	if c.handyV4 {
		packet = c.handyPacket(744, handyV4PositionPayload(float32(pos), ms, hold))
	} else {
		vector := append(pv(2, ms), pd(3, pos)...)
		packet = c.handyPacket(403, pb(3, vector))
	}
	from := c.handyPosition()
	if e := c.handyWrite(packet); e != nil {
		return e
	}
	c.handyFrom = from
	c.handyTarget = pos
	c.handyStart = time.Now()
	c.handyDuration = duration
	return nil
}
func (c *Controller) handyStop() error {
	if c.characteristic == nil || c.handyDuration == 0 {
		return nil
	}
	if c.handyV4 {
		if e := c.handyWrite(c.handyPacket(746, nil)); e != nil {
			return e
		}
	} else {
		if e := c.handyMove(c.handyPosition(), time.Millisecond, true); e != nil {
			return e
		}
	}
	c.handyDuration = 0
	return nil
}

// FollowScript refreshes the playback heartbeat; endpoint writes use their own timer.
func (c *Controller) FollowScript(actions []script.Action, ms int64, rate float64) error {
	return c.call(func() error {
		if !c.handy {
			return errors.New("当前设备不支持位置控制")
		}
		if len(actions) < 2 || rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
			return errors.New("脚本播放参数无效")
		}
		now := time.Now()
		c.last = now
		c.active = true
		if c.handyClock != nil && c.handyClock.Matches(actions, ms, rate, now) {
			return nil
		}
		if c.handyClock != nil && len(c.handyClock.Actions) == len(actions) && &c.handyClock.Actions[0] == &actions[0] {
			// Correct future scheduling without cancelling an issued move or hold.
			c.handyClock.MS = ms
			c.handyClock.Rate = rate
			c.handyClock.Anchor = now
			return nil
		}
		if e := c.handyStop(); e != nil {
			return e
		}
		c.handyClock = &script.MotionClock{Actions: actions, MS: ms, Rate: rate, Anchor: now}
		diagnostics.Event("device.handy.schedule", map[string]any{"points": len(actions), "time_ms": ms, "rate": rate})
		if e := c.advanceHandyScript(); e != nil {
			c.release(false, "Handy 蓝牙写入失败，已断开")
			return e
		}
		return nil
	})
}

func (c *Controller) advanceHandyScript() error {
	if c.handyClock == nil {
		return nil
	}
	pos, duration, deadline, ok := c.handyClock.Next(time.Now())
	if !ok {
		return c.stopStroke()
	}
	if e := c.handyMove(pos, duration, c.handyClock.Hold); e != nil {
		return e
	}
	if c.handyClock.Hold {
		diagnostics.Event("device.handy.hold", map[string]any{"position": pos * 100, "arrival_ms": duration.Milliseconds(), "hold_remaining_ms": time.Until(deadline).Milliseconds()})
	}
	c.handyNext = deadline
	return nil
}

// End of media drains already queued endpoints; explicit Stop still cancels them.
func (c *Controller) FinishHandyScript() error {
	return c.call(func() error {
		if c.handy && c.handyClock != nil && c.active {
			c.handyClock.Finish(time.Now())
			c.handyDraining = true
		}
		return nil
	})
}

func (c *Controller) TestHandyPosition(position int) error {
	if position != 0 && position != 100 {
		return errors.New("测试位置只能是 0 或 100")
	}
	return c.call(func() error {
		if !c.handy || c.dev == nil {
			return errors.New("请先连接 Handy 蓝牙")
		}
		if e := c.stopStroke(); e != nil {
			return e
		}
		if e := c.handyMove(float64(position)/100, 500*time.Millisecond, true); e != nil {
			c.release(false, "Handy 蓝牙写入失败，已断开")
			return e
		}
		c.active = true
		c.last = time.Now()
		c.testUntil = c.last.Add(time.Second)
		diagnostics.Event("device.handy.endpoint_test", map[string]any{"position": position})
		return nil
	})
}
