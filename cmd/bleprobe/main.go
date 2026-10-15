package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"

	"tinygo.org/x/bluetooth"
)

// This probe only discovers GATT services. It never writes movement commands.
func main() {
	runtime.LockOSThread()
	address := flag.String("address", "20:3F:70:73:72:BE", "BLE device address")
	pulse := flag.Bool("pulse", false, "test the observed low stroke command for two seconds, then stop")
	flag.Parse()
	if err := discover(*address, *pulse); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func discover(address string, pulse bool) error {
	adapter := bluetooth.DefaultAdapter
	if err := adapter.Enable(); err != nil {
		return fmt.Errorf("enable Bluetooth: %w", err)
	}
	mac, err := bluetooth.ParseMAC(address)
	if err != nil {
		return err
	}
	fmt.Println("Connecting directly through Windows Bluetooth:", address)
	device, err := adapter.Connect(bluetooth.Address{MACAddress: bluetooth.MACAddress{MAC: mac}}, bluetooth.ConnectionParams{})
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer device.Disconnect()
	services, err := device.DiscoverServices(nil)
	if err != nil {
		return fmt.Errorf("discover services: %w", err)
	}
	tested := false
	for _, service := range services {
		fmt.Println("service", service.UUID().String())
		characteristics, err := service.DiscoverCharacteristics(nil)
		if err != nil {
			return fmt.Errorf("discover characteristics: %w", err)
		}
		for _, characteristic := range characteristics {
			fmt.Println("  characteristic", characteristic.UUID().String())
			if pulse && characteristic.UUID().String() == "0000ee01-0000-1000-8000-00805f9b34fb" {
				tested = true
				stop := func() error {
					_, e1 := characteristic.WriteWithoutResponse([]byte{0xac, 3, 0, 0, 0, 0})
					_, e2 := characteristic.WriteWithoutResponse([]byte{0xac, 1, 0, 0, 0, 0})
					if e1 != nil {
						return e1
					}
					return e2
				}
				defer stop()
				if err := stop(); err != nil {
					return fmt.Errorf("initial stop: %w", err)
				}
				if _, err := characteristic.WriteWithoutResponse([]byte{0xac, 3, 1, 0, 1, 0x38}); err != nil {
					return fmt.Errorf("low stroke: %w", err)
				}
				fmt.Println("Low stroke write succeeded; stopping in 2 seconds")
				time.Sleep(2 * time.Second)
				if err := stop(); err != nil {
					return fmt.Errorf("stop: %w", err)
				}
				fmt.Println("Both stop writes succeeded")
			}
		}
	}
	if pulse && !tested {
		return fmt.Errorf("target ee01 characteristic was not found; no pulse was sent")
	}
	return nil
}
