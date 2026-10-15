package device

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"syncplayer/internal/diagnostics"
	"time"
	"tinygo.org/x/bluetooth"
)

type DiscoveredDevice struct {
	Name     string `json:"name"`
	Address  string `json:"address"`
	Identity string `json:"identity"`
	Signal   int16  `json:"signal"`
}

// Merge advertisements and scan responses: the name and identifier can arrive separately.
type advertisementRecord struct {
	device      DiscoveredDevice
	identifiers map[string]bool
}

func handySerialIdentity(name string) string {
	n := strings.ToLower(name)
	parts := strings.SplitN(n, "_", 3)
	if !supportedHandy(name) || len(parts) != 3 || !strings.HasPrefix(n, "ohd_hw") || len(parts[2]) < 6 {
		return ""
	}
	if _, err := hex.DecodeString(parts[2]); err != nil {
		return ""
	}
	return "handy:" + n
}

func (c *Controller) discover(name, identity string) ([]DiscoveredDevice, error) {
	diagnostics.Event("device.scan.begin", map[string]any{"resolve": identity != ""})
	devices := []DiscoveredDevice{}
	err := c.call(func() error {
		if c.dev != nil {
			return errors.New("请先断开设备再扫描")
		}
		adapter := bluetooth.DefaultAdapter
		if !c.enabled {
			if err := adapter.Enable(); err != nil {
				return err
			}
			c.enabled = true
		}
		var mu sync.Mutex
		var stopOnce sync.Once
		records := map[string]*advertisementRecord{}
		err := adapter.ScanFor(func(_ *bluetooth.Adapter, result bluetooth.ScanResult) {
			if result.AdvertisementPayload == nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			address := result.Address.String()
			record := records[address]
			if record == nil {
				record = &advertisementRecord{device: DiscoveredDevice{Address: address}, identifiers: map[string]bool{}}
				records[address] = record
			}
			if n := result.LocalName(); n != "" {
				record.device.Name = n
			}
			if supportedHandy(record.device.Name) {
				record.identifiers["address:"+address] = true
			}
			if serial := handySerialIdentity(record.device.Name); serial != "" {
				record.identifiers[serial] = true
			}
			record.device.Signal = result.RSSI
			for _, data := range result.ServiceData() {
				if len(data.Data) > 0 {
					record.identifiers["service:"+data.UUID.String()+":"+hex.EncodeToString(data.Data)] = true
				}
			}
			for _, data := range result.ManufacturerData() {
				if len(data.Data) > 0 {
					record.identifiers[fmt.Sprintf("manufacturer:%04x:%s", data.CompanyID, hex.EncodeToString(data.Data))] = true
				}
			}
			// An exact address identifies a single Handy; full list scans and
			// non-address identities retain duplicate detection across the window.
			if identity != "" && strings.EqualFold(record.device.Name, name) && ((strings.HasPrefix(identity, "address:") && record.identifiers[identity]) || handySerialIdentity(name) != "") {
				stopOnce.Do(func() { _ = adapter.StopScan() })
			}
		}, 6*time.Second)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		for _, record := range records {
			// This is the only model whose protocol has been captured and implemented.
			if !strings.EqualFold(record.device.Name, "FN010-RX") && !supportedHandy(record.device.Name) {
				continue
			}
			keys := []string{}
			for key := range record.identifiers {
				keys = append(keys, key)
			}
			if len(keys) == 0 {
				continue
			}
			sort.Strings(keys)
			record.device.Identity = keys[0]
			// Prefer service data; manufacturer data is a fallback identifier.
			for _, key := range keys {
				if strings.HasPrefix(key, "service:") {
					record.device.Identity = key
					break
				}
			}
			if serial := handySerialIdentity(record.device.Name); serial != "" {
				record.device.Identity = serial
			}
			if identity != "" {
				if !strings.EqualFold(record.device.Name, name) || (!record.identifiers[identity] && handySerialIdentity(name) == "") {
					continue
				}
				record.device.Identity = identity
			}
			devices = append(devices, record.device)
		}
		sort.Slice(devices, func(i, j int) bool {
			if devices[i].Signal == devices[j].Signal {
				return devices[i].Identity < devices[j].Identity
			}
			return devices[i].Signal > devices[j].Signal
		})
		return nil
	})
	diagnostics.Event("device.scan.result", map[string]any{"count": len(devices), "error": err, "resolve": identity != ""})
	return devices, err
}

func (c *Controller) Discover() ([]DiscoveredDevice, error) { return c.discover("", "") }

func (c *Controller) ConnectDevice(name, identity string) error {
	if (!strings.EqualFold(name, "FN010-RX") && !supportedHandy(name)) || identity == "" {
		return errors.New("请选择支持的设备")
	}
	devices, err := c.discover(name, identity)
	if err != nil {
		return err
	}
	if len(devices) == 0 {
		return errors.New("未找到所选设备，请确认设备已开机且未被其他程序连接")
	}
	if len(devices) != 1 {
		return errors.New("发现重复设备标识，请关闭其他同型号设备后重试")
	}
	return c.Connect(devices[0].Address)
}
