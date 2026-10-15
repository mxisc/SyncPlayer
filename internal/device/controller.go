package device

import (
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"syncplayer/internal/diagnostics"
	"syncplayer/internal/script"
	"time"
	"tinygo.org/x/bluetooth"
)

type request struct {
	fn   func() error
	done chan error
}
type Controller struct {
	lost                   chan bluetooth.Device
	state                  atomic.Pointer[State]
	queue                  chan request
	dev                    *bluetooth.Device
	characteristic         *bluetooth.DeviceCharacteristic
	last                   time.Time
	active                 bool
	level                  int
	mode                   string
	epoch                  time.Time
	on                     bool
	writeLogs              map[string]time.Time
	enabled                bool
	patterned              bool
	testUntil              time.Time
	cycleTimed             bool
	vibrationLevel         int
	vibrationLast          time.Time
	handy                  bool
	handyV4                bool
	handySeq               uint64
	handyPing              time.Time
	handyStart             time.Time
	handyDuration          time.Duration
	handyFrom, handyTarget float64
	handyReplies           chan []byte
	handyClock             *script.MotionClock
	handyNext              time.Time
	handyDraining          bool
}

type State struct {
	Connected bool   `json:"connected"`
	Message   string `json:"message"`
	Kind      string `json:"kind"`
}

func (c *Controller) State() State { return *c.state.Load() }
func (c *Controller) publish(connected bool, message string) {
	diagnostics.Event("device.state", map[string]any{"connected": connected, "message": message})
	kind := "bluetooth"
	if c.handy {
		kind = "handy-bluetooth"
	}
	c.state.Store(&State{Connected: connected, Message: message, Kind: kind})
}

func New() *Controller {
	c := &Controller{queue: make(chan request), lost: make(chan bluetooth.Device, 8), writeLogs: make(map[string]time.Time)}
	c.publish(false, "设备未连接")
	go c.loop()
	return c
}
func (c *Controller) loop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	bluetooth.DefaultAdapter.SetConnectHandler(func(d bluetooth.Device, connected bool) {
		if !connected {
			select {
			case c.lost <- d:
			default:
			}
		}
	})
	timer := time.NewTicker(10 * time.Millisecond)
	check := time.NewTicker(500 * time.Millisecond)
	defer check.Stop()
	defer timer.Stop()
	motionTimer := time.NewTimer(time.Hour)
	motionTimer.Stop()
	defer motionTimer.Stop()
	for {
		var motionC <-chan time.Time
		if c.handy && c.active && !c.handyNext.IsZero() {
			motionTimer.Reset(max(time.Nanosecond, time.Until(c.handyNext)))
			motionC = motionTimer.C
		} else {
			motionTimer.Stop()
		}
		select {
		case <-motionC:
			if !c.handyDraining && time.Since(c.last) > 750*time.Millisecond {
				if c.stop() != nil {
					c.release(false, "停止指令发送失败，连接已断开")
				}
			} else if e := c.advanceHandyScript(); e != nil {
				c.release(false, "Handy 蓝牙写入失败，已断开")
			}
		case r := <-c.queue:
			r.done <- r.fn()
		case d := <-c.lost:
			diagnostics.Event("device.native_event", map[string]any{"connected": false, "current_connection": c.dev != nil && c.dev.SameConnection(d)})
			if c.dev != nil && c.dev.SameConnection(d) {
				c.release(false, "设备连接已断开，请重新连接")
			}
		case <-check.C:
			if c.dev != nil {
				connected, e := c.dev.Connected()
				if e != nil || !connected {
					diagnostics.Event("device.poll_lost", map[string]any{"error": e, "connected": connected})
					c.release(false, "设备连接已断开，请重新连接")
				}
			}
			if c.handy && c.dev != nil && time.Since(c.handyPing) > time.Second {
				if e := c.handyKeepalive(); e != nil {
					c.release(false, "Handy 蓝牙连接已断开")
				}
			}
		case <-timer.C:
			if c.vibrationLevel > 0 && time.Since(c.vibrationLast) > 750*time.Millisecond {
				if c.setVibration(0) != nil {
					c.release(false, "震动停止失败，连接已断开")
				}
			}
			if !c.testUntil.IsZero() && !time.Now().Before(c.testUntil) {
				diagnostics.Event("device.timed_test.end", nil)
				if c.stopStroke() != nil {
					c.release(false, "测试停止失败，连接已断开")
				}
			} else if c.active && !c.handyDraining && (c.testUntil.IsZero() || c.cycleTimed) && time.Since(c.last) > 750*time.Millisecond {
				diagnostics.Event("device.heartbeat_timeout", map[string]any{"elapsed_ms": time.Since(c.last).Milliseconds()})
				if c.stop() != nil {
					c.release(false, "停止指令发送失败，连接已断开")
				}
			} else if c.active && !c.handy {
				if c.apply() != nil {
					c.release(false, "蓝牙写入失败，连接已断开")
				}
			}
		}
	}
}
func (c *Controller) call(fn func() error) error {
	r := request{fn, make(chan error, 1)}
	c.queue <- r
	return <-r.done
}
func (c *Controller) Connect(address string) error {
	diagnostics.Event("device.connect.request", nil)
	e := c.call(func() error {
		if c.dev != nil {
			c.release(true, "设备已断开")
		}
		a := bluetooth.DefaultAdapter
		if !c.enabled {
			diagnostics.Event("device.adapter.enable.begin", nil)
			e := a.Enable()
			diagnostics.Event("device.adapter.enable.end", map[string]any{"error": e})
			if e != nil {
				return e
			}
			c.enabled = true
		}
		mac, e := bluetooth.ParseMAC(address)
		if e != nil {
			return e
		}
		d, e := a.Connect(bluetooth.Address{MACAddress: bluetooth.MACAddress{MAC: mac}}, bluetooth.ConnectionParams{})
		if e != nil {
			return e
		}
		c.dev = &d
		diagnostics.Event("device.connect.gatt", nil)
		success := false
		defer func() {
			if !success {
				c.release(false, "连接失败，请重试")
			}
		}()
		services, e := d.DiscoverServices(nil)
		diagnostics.Event("device.discover.services", map[string]any{"count": len(services), "error": e})
		if e != nil {
			return e
		}
		for _, s := range services {
			if s.UUID().String() == handyService || s.UUID().String() == handyV4Service {
				if e := c.connectHandy(s); e != nil {
					return e
				}
				success = true
				c.publish(true, "Handy 蓝牙已连接")
				return nil
			}
			if s.UUID().String() != "0000ffee-0000-1000-8000-00805f9b34fb" {
				continue
			}
			chars, e := s.DiscoverCharacteristics(nil)
			diagnostics.Event("device.discover.characteristics", map[string]any{"count": len(chars), "error": e})
			if e != nil {
				return e
			}
			for _, ch := range chars {
				if ch.UUID().String() == "0000ee01-0000-1000-8000-00805f9b34fb" {
					c.characteristic = &ch
					if e := c.stop(); e != nil {
						return e
					}
					success = true
					c.publish(true, "设备已连接")
					return nil
				}
			}
		}
		return errors.New("未发现 ee01 写入特征，请退出其他控制软件后重试")
	})
	diagnostics.Event("device.connect.result", map[string]any{"error": e})
	return e
}
func (c *Controller) write(data []byte) error {
	if c.handy {
		return errors.New("Handy 使用位置控制，不支持 FN010 档位指令")
	}
	if c.characteristic == nil {
		return errors.New("设备未连接")
	}
	command := fmt.Sprintf("%x", data)
	record := time.Since(c.writeLogs[command]) > 2*time.Second
	if record {
		diagnostics.Event("device.write.begin", map[string]any{"command": command, "mode": c.mode, "level": c.level})
		c.writeLogs[command] = time.Now()
	}
	n, e := c.characteristic.WriteWithoutResponse(data)
	if record || e != nil {
		diagnostics.Event("device.write.end", map[string]any{"command": command, "bytes": n, "error": e})
	}
	if e != nil {
		return e
	}
	if n != len(data) {
		return fmt.Errorf("short write: %d/%d", n, len(data))
	}
	return nil
}
func (c *Controller) stopStroke() error {
	c.handyDraining = false
	c.handyClock = nil
	c.handyNext = time.Time{}
	c.testUntil = time.Time{}
	c.cycleTimed = false
	c.active = false
	c.on = false
	if c.handy {
		return c.handyStop()
	}
	if c.characteristic == nil {
		return nil
	}
	return c.write([]byte{0xac, 3, 0, 0, 0, 0})
}
func (c *Controller) stop() error {
	e := c.stopStroke()
	c.vibrationLevel = 0
	if c.handy {
		return e
	}
	if c.characteristic == nil {
		return e
	}
	return errors.Join(e, c.write([]byte{0xac, 1, 0, 0, 0, 0}))
}
func (c *Controller) StopStroke() error {
	return c.call(func() error {
		e := c.stopStroke()
		if e != nil {
			c.release(false, "伸缩停止失败，连接已断开")
		}
		return e
	})
}
func (c *Controller) setVibration(level int) error {
	if c.handy {
		if level != 0 {
			return errors.New("Handy 不支持震动通道")
		}
		return nil
	}
	if level < 0 || level > 10 {
		return errors.New("震动档位无效")
	}
	c.vibrationLast = time.Now()
	if level == c.vibrationLevel {
		return nil
	}
	if c.characteristic == nil {
		return errors.New("设备未连接")
	}
	speed := []byte{0, 0x1f, 0x38, 0x51, 0x6a, 0x83, 0x9b, 0xb4, 0xcd, 0xe6, 0xff}[level]
	cmd := []byte{0xac, 1, 1, 0, 1, speed}
	if level == 0 {
		cmd = []byte{0xac, 1, 0, 0, 0, 0}
	}
	if e := c.write(cmd); e != nil {
		return e
	}
	c.vibrationLevel = level
	return nil
}
func (c *Controller) Vibration(level int) error {
	return c.call(func() error {
		e := c.setVibration(level)
		if e != nil {
			c.release(false, "震动写入失败，连接已断开")
		}
		return e
	})
}
func (c *Controller) Stop() error {
	return c.call(func() error {
		e := c.stop()
		if e != nil {
			c.release(false, "停止指令发送失败，连接已断开")
		}
		return e
	})
}

// Clear owned references before cleanup. A lost link must not receive stop
// writes, and repeated requests must never close the same COM object twice.
func (c *Controller) release(sendStop bool, message string) error {
	diagnostics.Event("device.release.begin", map[string]any{"send_stop": sendStop, "has_device": c.dev != nil, "reason": message})
	var e error
	if sendStop && c.dev != nil {
		connected, checkErr := c.dev.Connected()
		if checkErr == nil && connected {
			e = c.stop()
		}
	}
	d := c.dev
	c.dev = nil
	c.characteristic = nil
	c.handy = false
	c.handyV4 = false
	c.handyReplies = nil
	c.handyDraining = false
	c.handyClock = nil
	c.handyNext = time.Time{}
	c.active = false
	c.on = false
	c.vibrationLevel = 0
	c.publish(false, message)
	c.testUntil = time.Time{}
	if d != nil {
		e = errors.Join(e, d.Disconnect())
	}
	diagnostics.Event("device.release.end", map[string]any{"error": e})
	return e
}
func (c *Controller) Disconnect() error {
	return c.call(func() error {
		return c.release(true, "设备已断开")
	})
}
func (c *Controller) Level(level int, mode string) error {
	return c.setLevel(level, mode, true)
}

func (c *Controller) TimedTest(level int, mode string, seconds float64) error {
	if seconds < 0.1 || seconds > 60 || level < 1 || level > 10 {
		return errors.New("测试档位为 1–10，时长为 0.1–60 秒")
	}
	if e := c.Motion(level, mode); e != nil {
		return e
	}
	return c.call(func() error {
		if !c.active {
			return errors.New("测试已取消或设备未连接")
		}
		c.testUntil = time.Now().Add(time.Duration(seconds * float64(time.Second)))
		diagnostics.Event("device.timed_test.begin", map[string]any{"level": level, "mode": mode, "seconds": seconds, "continuous": true})
		return nil
	})
}

func (c *Controller) BeginCycle(level int, seconds float64) error {
	if e := c.TimedTest(level, "stroke", seconds); e != nil {
		return e
	}
	return c.call(func() error { c.cycleTimed = true; c.last = time.Now(); return nil })
}

func (c *Controller) CycleHeartbeat() error {
	return c.call(func() error {
		if c.dev == nil {
			return errors.New("设备未连接")
		}
		c.last = time.Now()
		return nil
	})
}

// Motion runs the selected speed continuously, without software pauses.
func (c *Controller) Motion(level int, mode string) error {
	return c.setLevel(level, mode, false)
}

func (c *Controller) stopForMode(mode string) error {
	if mode == "stroke" {
		return c.stopStroke()
	}
	return c.stop()
}
func (c *Controller) setLevel(level int, mode string, patterned bool) error {
	return c.call(func() error {
		if level < 0 || level > 10 {
			return errors.New("档位必须在 0 到 10 之间")
		}
		if mode != "stroke" && mode != "vibration" {
			return errors.New("未知模式")
		}
		if level == 0 {
			e := c.stopForMode(mode)
			if e != nil {
				c.release(false, "蓝牙写入失败，连接已断开")
			}
			return e
		}
		c.last = time.Now()
		c.testUntil = time.Time{}
		c.cycleTimed = false
		if !c.active || c.level != level || c.mode != mode || c.patterned != patterned {
			if e := c.stopForMode(mode); e != nil {
				c.release(false, "蓝牙写入失败，连接已断开")
				return e
			}
			c.level = level
			c.mode = mode
			c.patterned = patterned
			c.epoch = time.Now()
		}
		c.active = true
		e := c.apply()
		if e != nil {
			c.release(false, "蓝牙写入失败，连接已断开")
		}
		return e
	})
}

func (c *Controller) apply() error {
	level, mode := c.level, c.mode
	shouldRun := true
	if c.patterned && mode == "stroke" && level <= 3 {
		run := []time.Duration{0, 400, 400, 200}[level] * time.Millisecond
		pause := []time.Duration{0, 1500, 800, 200}[level] * time.Millisecond
		shouldRun = time.Since(c.epoch)%(run+pause) < run
	}
	if shouldRun == c.on {
		return nil
	}
	var speed byte
	motor := byte(3)
	if mode == "stroke" {
		speed = []byte{0, 0x38, 0x4e, 0x64, 0x38, 0x64, 0xa7, 0xbd, 0xd3, 0xe9, 0xff}[level]
	} else {
		motor = 1
		speed = []byte{0, 0x1f, 0x38, 0x51, 0x6a, 0x83, 0x9b, 0xb4, 0xcd, 0xe6, 0xff}[level]
	}
	command := []byte{0xac, motor, 1, 0, 1, speed}
	if !shouldRun {
		command = []byte{0xac, motor, 0, 0, 0, 0}
	}
	if e := c.write(command); e != nil {
		return e
	}
	c.on = shouldRun
	return nil
}
