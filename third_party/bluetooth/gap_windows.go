package bluetooth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/saltosystems/winrt-go"
	"github.com/saltosystems/winrt-go/windows/devices/bluetooth"
	"github.com/saltosystems/winrt-go/windows/devices/bluetooth/advertisement"
	"github.com/saltosystems/winrt-go/windows/devices/bluetooth/genericattributeprofile"
	"github.com/saltosystems/winrt-go/windows/foundation"
	"github.com/saltosystems/winrt-go/windows/storage/streams"
)

// Address contains a Bluetooth MAC address.
type Address struct {
	MACAddress
}

type Advertisement struct {
	advertisement *advertisement.BluetoothLEAdvertisement
	publisher     *advertisement.BluetoothLEAdvertisementPublisher
}

// DefaultAdvertisement returns the default advertisement instance but does not
// configure it.
func (a *Adapter) DefaultAdvertisement() *Advertisement {
	if a.defaultAdvertisement == nil {
		a.defaultAdvertisement = &Advertisement{}
	}

	return a.defaultAdvertisement
}

// Configure this advertisement.
// on Windows we're only able to set "Manufacturer Data" for advertisements.
// https://learn.microsoft.com/en-us/uwp/api/windows.devices.bluetooth.advertisement.bluetoothleadvertisementpublisher?view=winrt-22621#remarks
// following this c# source for this implementation: https://github.com/microsoft/Windows-universal-samples/blob/main/Samples/BluetoothAdvertisement/cs/Scenario2_Publisher.xaml.cs
// adding service data / localname leads to errors when starting the advertisement.
func (a *Advertisement) Configure(options AdvertisementOptions) error {
	// we can only advertise manufacturer / company data on windows, so no need to continue if we have none
	if len(options.ManufacturerData) == 0 {
		return nil
	}

	if a.publisher != nil {
		a.publisher.Release()
	}

	if a.advertisement != nil {
		a.advertisement.Release()
	}

	pub, err := advertisement.NewBluetoothLEAdvertisementPublisher()
	if err != nil {
		return err
	}

	a.publisher = pub

	ad, err := a.publisher.GetAdvertisement()
	if err != nil {
		return err
	}

	a.advertisement = ad

	vec, err := ad.GetManufacturerData()
	if err != nil {
		return err
	}

	for _, optManData := range options.ManufacturerData {
		writer, err := streams.NewDataWriter()
		if err != nil {
			return err
		}
		defer writer.Release()

		err = writer.WriteBytes(uint32(len(optManData.Data)), optManData.Data)
		if err != nil {
			return err
		}

		buf, err := writer.DetachBuffer()
		if err != nil {
			return err
		}

		manData, err := advertisement.BluetoothLEManufacturerDataCreate(optManData.CompanyID, buf)
		if err != nil {
			return err
		}

		if err = vec.Append(unsafe.Pointer(&manData.IUnknown.RawVTable)); err != nil {
			return err
		}
	}

	return nil
}

// Start advertisement. May only be called after it has been configured.
func (a *Advertisement) Start() error {
	// publisher will be present if we actually have manufacturer data to advertise.
	if a.publisher != nil {
		return a.publisher.Start()
	}

	return nil
}

// Stop advertisement. May only be called after it has been started.
func (a *Advertisement) Stop() error {
	if a.publisher != nil {
		return a.publisher.Stop()
	}

	return nil
}

// Scan starts a BLE scan. It is stopped by a call to StopScan. A common pattern
// is to cancel the scan when a particular device has been found.
func (a *Adapter) Scan(callback func(*Adapter, ScanResult)) (err error) {
	return a.scanFor(callback, 0)
}

// ScanFor bounds discovery without moving WinRT watcher calls off its owner thread.
func (a *Adapter) ScanFor(callback func(*Adapter, ScanResult), duration time.Duration) error {
	return a.scanFor(callback, duration)
}

func (a *Adapter) scanFor(callback func(*Adapter, ScanResult), duration time.Duration) (err error) {
	if a.watcher != nil {
		// Cannot scan more than once: which one should ScanStop()
		// stop?
		return errScanning
	}

	a.watcher, err = advertisement.NewBluetoothLEAdvertisementWatcher()
	if err != nil {
		return
	}
	defer func() {
		_ = a.watcher.Release()
		a.watcher = nil
	}()

	// Set scanning mode to active so we receive scan responses
	// from devices in advertising mode
	err = a.watcher.SetScanningMode(advertisement.BluetoothLEScanningModeActive)
	if err != nil {
		return
	}

	// Listen for incoming BLE advertisement packets.
	// We need a TypedEventHandler<TSender, TResult> to listen to events, but since this is a parameterized delegate
	// its GUID depends on the classes used as sender and result, so we need to compute it:
	// TypedEventHandler<BluetoothLEAdvertisementWatcher, BluetoothLEAdvertisementReceivedEventArgs>
	eventReceivedGuid := winrt.ParameterizedInstanceGUID(
		foundation.GUIDTypedEventHandler,
		advertisement.SignatureBluetoothLEAdvertisementWatcher,
		advertisement.SignatureBluetoothLEAdvertisementReceivedEventArgs,
	)
	handler := foundation.NewTypedEventHandler(ole.NewGUID(eventReceivedGuid), func(instance *foundation.TypedEventHandler, sender, arg unsafe.Pointer) {
		args := (*advertisement.BluetoothLEAdvertisementReceivedEventArgs)(arg)
		result := getScanResultFromArgs(args)
		callback(a, result)
	})
	defer handler.Release()

	token, err := a.watcher.AddReceived(handler)
	if err != nil {
		return
	}
	defer a.watcher.RemoveReceived(token)

	// Wait for when advertisement has stopped by a call to StopScan().
	// Advertisement doesn't seem to stop right away, there is an
	// intermediate Stopping state.
	stoppingChan := make(chan error, 1)
	// TypedEventHandler<BluetoothLEAdvertisementWatcher, BluetoothLEAdvertisementWatcherStoppedEventArgs>
	eventStoppedGuid := winrt.ParameterizedInstanceGUID(
		foundation.GUIDTypedEventHandler,
		advertisement.SignatureBluetoothLEAdvertisementWatcher,
		advertisement.SignatureBluetoothLEAdvertisementWatcherStoppedEventArgs,
	)
	stoppedHandler := foundation.NewTypedEventHandler(ole.NewGUID(eventStoppedGuid), func(_ *foundation.TypedEventHandler, _, arg unsafe.Pointer) {
		args := (*advertisement.BluetoothLEAdvertisementWatcherStoppedEventArgs)(arg)
		errCode, err := args.GetError()
		if err != nil {
			// Got an error while getting the error value, that shouldn't
			// happen.
			stoppingChan <- fmt.Errorf("failed to get stopping error value: %w", err)
		} else if errCode != bluetooth.BluetoothErrorSuccess {
			// Could not stop the scan? I'm not sure when this would actually
			// happen.
			stoppingChan <- fmt.Errorf("failed to stop scanning (error code %d)", errCode)
		}
		close(stoppingChan)
	})
	defer stoppedHandler.Release()

	token, err = a.watcher.AddStopped(stoppedHandler)
	if err != nil {
		return
	}
	defer a.watcher.RemoveStopped(token)

	err = a.watcher.Start()
	if err != nil {
		return err
	}

	// Wait until advertisement has stopped, and finish.
	if duration > 0 {
		timer := time.NewTimer(duration)
		defer timer.Stop()
		select {
		case err := <-stoppingChan:
			return err
		case <-timer.C:
			if err := a.watcher.Stop(); err != nil {
				return err
			}
		}
	}
	return <-stoppingChan
}

func getScanResultFromArgs(args *advertisement.BluetoothLEAdvertisementReceivedEventArgs) ScanResult {
	// parse bluetooth address
	addr, _ := args.GetBluetoothAddress()
	adr := Address{}
	for i := range adr.MAC {
		adr.MAC[i] = byte(addr)
		addr >>= 8
	}
	sigStrength, _ := args.GetRawSignalStrengthInDBm()
	result := ScanResult{
		RSSI:    sigStrength,
		Address: adr,
	}

	winAdv, err := args.GetAdvertisement()
	if err != nil {
		return result
	}
	defer winAdv.Release()

	var manufacturerData []ManufacturerDataElement
	var serviceUUIDs []UUID
	var serviceData []ServiceDataElement
	sections, _ := winAdv.GetDataSections()
	if sections != nil {
		defer sections.Release()
		size, _ := sections.GetSize()
		for i := uint32(0); i < size; i++ {
			element, err := sections.GetAt(i)
			if err != nil || element == nil {
				continue
			}
			section := (*advertisement.BluetoothLEAdvertisementDataSection)(element)
			kind, err := section.GetDataType()
			if err == nil && (kind == 0x16 || kind == 0x20 || kind == 0x21) {
				data := advertisementSectionData(section)
				var uuid UUID
				prefix := 0
				switch {
				case kind == 0x16 && len(data) >= 2:
					uuid = New16BitUUID(uint16(data[0]) | uint16(data[1])<<8)
					prefix = 2
				case kind == 0x20 && len(data) >= 4:
					uuid = New32BitUUID(uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16 | uint32(data[3])<<24)
					prefix = 4
				case kind == 0x21 && len(data) >= 16:
					var bytes [16]byte
					for j := range bytes {
						bytes[j] = data[15-j]
					}
					uuid = NewUUID(bytes)
					prefix = 16
				}
				if prefix > 0 {
					serviceData = append(serviceData, ServiceDataElement{UUID: uuid, Data: data[prefix:]})
				}
			}
			section.Release()
		}
	}

	// Extract manufacturer data
	manDataVector, _ := winAdv.GetManufacturerData()
	if manDataVector != nil {
		defer manDataVector.Release()
		size, _ := manDataVector.GetSize()
		for i := uint32(0); i < size; i++ {
			element, _ := manDataVector.GetAt(i)
			manData := (*advertisement.BluetoothLEManufacturerData)(element)

			companyID, _ := manData.GetCompanyId()
			buffer, _ := manData.GetData()
			if buffer != nil {
				manufacturerData = append(manufacturerData, ManufacturerDataElement{
					CompanyID: companyID,
					Data:      bufferToSlice(buffer),
				})
				buffer.Release()
			}
			manData.Release()
		}
	}

	// Extract service UUIDs
	serviceUuidsVector, _ := winAdv.GetServiceUuids()
	if serviceUuidsVector != nil {
		defer serviceUuidsVector.Release()
		size, _ := serviceUuidsVector.GetSize()
		for i := uint32(0); i < size; i++ {
			// IVector<Guid>.GetAt writes 16 bytes, unlike the pointer-sized
			// generated IVector getter used for reference types.
			var serviceGUID syscall.GUID
			vt := (*[8]uintptr)(unsafe.Pointer(serviceUuidsVector.RawVTable))
			hr, _, _ := syscall.SyscallN(vt[6], uintptr(unsafe.Pointer(serviceUuidsVector)), uintptr(i), uintptr(unsafe.Pointer(&serviceGUID)))
			if hr != 0 {
				continue
			}
			uuid := GUIDToUUID(serviceGUID)
			serviceUUIDs = append(serviceUUIDs, uuid)
		}
	}

	// Note: the IsRandom bit is never set.
	localName, _ := winAdv.GetLocalName()
	result.AdvertisementPayload = &advertisementFields{
		AdvertisementFields{
			LocalName:        localName,
			ServiceUUIDs:     serviceUUIDs,
			ManufacturerData: manufacturerData,
			ServiceData:      serviceData,
		},
	}

	return result
}

func advertisementSectionData(section *advertisement.BluetoothLEAdvertisementDataSection) []byte {
	// winrt-go does not expose the Data getter on this generated interface.
	itf, err := section.QueryInterface(ole.NewGUID(advertisement.GUIDiBluetoothLEAdvertisementDataSection))
	if err != nil {
		return nil
	}
	defer itf.Release()
	var buffer *streams.IBuffer
	vt := (*[9]uintptr)(unsafe.Pointer(itf.RawVTable))
	hr, _, _ := syscall.SyscallN(vt[8], uintptr(unsafe.Pointer(itf)), uintptr(unsafe.Pointer(&buffer)))
	if hr != 0 || buffer == nil {
		return nil
	}
	defer buffer.Release()
	return bufferToSlice(buffer)
}

func GUIDToUUID(guid syscall.GUID) UUID {
	return NewUUID([16]byte{
		byte(guid.Data1 >> 24),
		byte(guid.Data1 >> 16),
		byte(guid.Data1 >> 8),
		byte(guid.Data1),
		byte(guid.Data2 >> 8),
		byte(guid.Data2),
		byte(guid.Data3 >> 8),
		byte(guid.Data3),
		guid.Data4[0], guid.Data4[1],
		guid.Data4[2], guid.Data4[3],
		guid.Data4[4], guid.Data4[5],
		guid.Data4[6], guid.Data4[7],
	})
}

func bufferToSlice(buffer *streams.IBuffer) []byte {
	dataReader, err := streams.DataReaderFromBuffer(buffer)
	if err != nil || dataReader == nil {
		return nil
	}
	defer dataReader.Release()
	bufferSize, err := buffer.GetLength()
	if err != nil {
		return nil
	}
	if bufferSize == 0 {
		return nil
	}
	data, _ := dataReader.ReadBytes(bufferSize)
	return data
}

// StopScan stops any in-progress scan. It can be called from within a Scan
// callback to stop the current scan. If no scan is in progress, an error will
// be returned.
func (a *Adapter) StopScan() error {
	if a.watcher == nil {
		return errNotScanning
	}
	return a.watcher.Stop()
}

var _ GAPDevice = Device{}

// Device is a connection to a remote peripheral.
type Device struct {
	lifecycle *deviceLifecycle
	ctx       context.Context
	cancel    context.CancelFunc

	Address Address // the MAC address of the device

	device                        *bluetooth.BluetoothLEDevice
	session                       *genericattributeprofile.GattSession
	connectionStatusListenerToken foundation.EventRegistrationToken
	connectionStatusListener      *foundation.TypedEventHandler
}

type deviceLifecycle struct {
	mu      sync.Mutex
	closed  bool
	cleanup []func() error
}

// SameConnection distinguishes stale disconnect events from a new connection
// to the same address. Device values share lifecycle ownership when copied.
func (d Device) SameConnection(other Device) bool {
	return d.lifecycle != nil && d.lifecycle == other.lifecycle
}

// Connect starts a connection attempt to the given peripheral device address.
//
// On Linux and Windows, the IsRandom part of the address is ignored.
func (a *Adapter) Connect(address Address, params ConnectionParams) (Device, error) {
	var winAddr uint64
	for i := range address.MAC {
		winAddr += uint64(address.MAC[i]) << (8 * i)
	}

	// IAsyncOperation<BluetoothLEDevice>
	bleDeviceOp, err := bluetooth.BluetoothLEDeviceFromBluetoothAddressAsync(winAddr)
	if err != nil {
		return Device{}, err
	}
	defer bleDeviceOp.Release()

	// We need to pass the signature of the parameter returned by the async operation:
	// IAsyncOperation<BluetoothLEDevice>
	if err := awaitAsyncOperation(bleDeviceOp, bluetooth.SignatureBluetoothLEDevice); err != nil {
		return Device{}, fmt.Errorf("error connecting to device: %w", err)
	}

	res, err := bleDeviceOp.GetResults()
	if err != nil {
		return Device{}, err
	}

	// The returned BluetoothLEDevice is set to null if FromBluetoothAddressAsync can't find the device identified by bluetoothAddress
	if uintptr(res) == 0x0 {
		return Device{}, fmt.Errorf("device with the given address was not found")
	}

	bleDevice := (*bluetooth.BluetoothLEDevice)(res)
	owned := false
	defer func() {
		if !owned {
			bleDevice.Close()
			bleDevice.Release()
		}
	}()

	// Creating a BluetoothLEDevice object by calling this method alone doesn't (necessarily) initiate a connection.
	// To initiate a connection, we need to set GattSession.MaintainConnection to true.
	dID, err := bleDevice.GetBluetoothDeviceId()
	if err != nil {
		return Device{}, err
	}
	defer dID.Release()

	// Windows does not support explicitly connecting to a device.
	// Instead it has the concept of a GATT session that is owned
	// by the calling program.
	gattSessionOp, err := genericattributeprofile.GattSessionFromDeviceIdAsync(dID) // IAsyncOperation<GattSession>
	if err != nil {
		return Device{}, err
	}
	defer gattSessionOp.Release()

	if err := awaitAsyncOperation(gattSessionOp, genericattributeprofile.SignatureGattSession); err != nil {
		return Device{}, fmt.Errorf("error getting gatt session: %w", err)
	}

	gattRes, err := gattSessionOp.GetResults()
	if err != nil {
		return Device{}, err
	}
	newSession := (*genericattributeprofile.GattSession)(gattRes)
	defer func() {
		if !owned {
			newSession.Close()
			newSession.Release()
		}
	}()
	// This keeps the device connected until we set maintain_connection = False.
	if err := newSession.SetMaintainConnection(true); err != nil {
		return Device{}, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	device := Device{
		lifecycle: &deviceLifecycle{},
		ctx:       ctx,
		cancel:    cancel,

		Address: address,

		device:  bleDevice,
		session: newSession,
	}

	// https://learn.microsoft.com/es-es/uwp/api/windows.devices.bluetooth.bluetoothledevice.connectionstatuschanged?view=winrt-26100
	// TypedEventHandler<BluetoothLEDevice,object>
	connectionStatusChangedGUID := winrt.ParameterizedInstanceGUID(
		foundation.GUIDTypedEventHandler,
		bluetooth.SignatureBluetoothLEDevice,
		"cinterface(IInspectable)", // object
	)

	handler := foundation.NewTypedEventHandler(ole.NewGUID(connectionStatusChangedGUID), func(instance *foundation.TypedEventHandler, sender, arg unsafe.Pointer) {
		// Never block WinRT's callback thread behind an asynchronous GATT
		// operation. The application also polls connection status.
		if !device.lifecycle.mu.TryLock() {
			return
		}
		if device.lifecycle.closed {
			device.lifecycle.mu.Unlock()
			return
		}
		status, err := bleDevice.GetConnectionStatus()
		device.lifecycle.mu.Unlock()
		if err != nil {
			return
		}
		if status == bluetooth.BluetoothConnectionStatusDisconnected {
			// Mark the shared context disconnected. The owner closes COM objects
			// on its worker thread, never from within a WinRT event callback.
			device.cancel()
		}

		if a.connectHandler != nil {
			a.connectHandler(device, status == bluetooth.BluetoothConnectionStatusConnected)
		}
	})

	token, err := device.device.AddConnectionStatusChanged(handler)

	device.connectionStatusListenerToken = token
	device.connectionStatusListener = handler

	if err != nil {
		_ = handler.Release()
		cancel()
		return device, err
	}

	owned = true
	return device, nil
}

// Disconnect from the BLE device. This method is non-blocking and does not
// wait until the connection is fully gone.
func (d Device) Disconnect() error {
	if d.lifecycle == nil {
		return nil
	}
	d.lifecycle.mu.Lock()
	if d.lifecycle.closed {
		d.lifecycle.mu.Unlock()
		return nil
	}
	d.lifecycle.closed = true
	defer d.lifecycle.mu.Unlock()
	defer d.device.Release()
	defer d.session.Release()
	if d.connectionStatusListener != nil {
		defer d.connectionStatusListener.Release()
	}

	d.cancel()

	// Attempt every cleanup operation even if the lost GATT session returns
	// an error. The shared closed flag prevents subsequent duplicate release.
	removeErr := d.device.RemoveConnectionStatusChanged(d.connectionStatusListenerToken)
	maintainErr := d.session.SetMaintainConnection(false)
	var cleanupErr error
	for i := len(d.lifecycle.cleanup) - 1; i >= 0; i-- {
		cleanupErr = errors.Join(cleanupErr, d.lifecycle.cleanup[i]())
	}
	d.lifecycle.cleanup = nil
	sessionErr := d.session.Close()
	deviceErr := d.device.Close()
	return errors.Join(removeErr, maintainErr, cleanupErr, sessionErr, deviceErr)
}

// Connected returns whether the device is currently connected.
func (d Device) Connected() (bool, error) {
	if d.lifecycle == nil {
		return false, nil
	}
	d.lifecycle.mu.Lock()
	defer d.lifecycle.mu.Unlock()
	if d.lifecycle.closed || d.ctx.Err() != nil {
		return false, nil
	}
	if d.device == nil {
		return false, nil
	}
	status, err := d.device.GetConnectionStatus()
	if err != nil {
		return false, err
	}
	return status == bluetooth.BluetoothConnectionStatusConnected, nil
}

// RequestConnectionParams requests a different connection latency and timeout
// of the given device connection. Fields that are unset will be left alone.
// Whether or not the device will actually honor this, depends on the device and
// on the specific parameters.
//
// On Windows, this call doesn't do anything.
func (d Device) RequestConnectionParams(params ConnectionParams) error {
	// TODO: implement this using
	// BluetoothLEDevice.RequestPreferredConnectionParameters.
	return nil
}

// SetRandomAddress sets the random address to be used for advertising.
func (a *Adapter) SetRandomAddress(mac MAC) error {
	return errors.ErrUnsupported
}
