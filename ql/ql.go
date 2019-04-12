// Package ql is a Linux driver for Brother QL-series printers.
package ql

// Resources:
//  http://www.undocprint.org/formats/page_description_languages/brother_p-touch

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// #include <linux/ioctl.h>
import "C"

// -----------------------------------------------------------------------------

func _IOC(dir, typ, nr, size int) uintptr {
	return (uintptr(dir) << C._IOC_DIRSHIFT) |
		(uintptr(typ) << C._IOC_TYPESHIFT) |
		(uintptr(nr) << C._IOC_NRSHIFT) |
		(uintptr(size) << C._IOC_SIZESHIFT)
}

const (
	iocnrGetDeviceID = 1
)

// lpiocGetDeviceID reads the IEEE-1284 Device ID string of a printer.
func lpiocGetDeviceID(fd uintptr) ([]byte, error) {
	var buf [1024]byte
	if _, _, err := syscall.Syscall(syscall.SYS_IOCTL, fd,
		_IOC(C._IOC_READ, 'P', iocnrGetDeviceID, len(buf)),
		uintptr(unsafe.Pointer(&buf))); err != 0 {
		return nil, err
	}

	// In theory it might get trimmed along the way.
	length := int(buf[0])<<8 | int(buf[1])
	if 2+length > len(buf) {
		return buf[2:], errors.New("the device ID string got trimmed")
	}

	return buf[2 : 2+length], nil
}

// -----------------------------------------------------------------------------

var deviceIDRegexp = regexp.MustCompile(
	`(?s:\s*([^:,;]+?)\s*:\s*([^:;]*)\s*(?:;|$))`)

type deviceID map[string][]string

// parseIEEE1284DeviceID leniently parses an IEEE 1284 Device ID string
// and returns a map containing a slice of values for each key.
//
// See e.g. http://www.undocprint.org/formats/communication_protocols/ieee_1284
func parseIEEE1284DeviceID(id []byte) deviceID {
	m := make(deviceID)
	for _, kv := range deviceIDRegexp.FindAllStringSubmatch(string(id), -1) {
		var values []string
		for _, v := range strings.Split(kv[2], ",") {
			values = append(values, strings.Trim(v, "\t\n\v\f\r "))
		}
		m[kv[1]] = values
	}
	return m
}

func (id deviceID) Find(key, abbreviation string) []string {
	if values, ok := id[key]; ok {
		return values
	}
	if values, ok := id[abbreviation]; ok {
		return values
	}
	return nil
}

// -----------------------------------------------------------------------------

type Printer struct {
	File *os.File
}

func compatible(id deviceID) bool {
	for _, commandSet := range id.Find("COMMAND SET", "CMD") {
		if commandSet == "PT-CBP" {
			return true
		}
	}
	return false
}

// Open finds and initializes the first USB printer found supporting
// the appropriate protocol. Returns nil if no printer could be found.
func Open() (*Printer, error) {
	// Linux usblp module, located in /drivers/usb/class/usblp.c
	paths, err := filepath.Glob("/dev/usb/lp[0-9]*")
	if err != nil {
		return nil, err
	}
	for _, candidate := range paths {
		f, err := os.OpenFile(candidate, os.O_RDWR, 0)
		if err != nil {
			continue
		}
		// Filter out obvious non-printers.
		deviceID, err := lpiocGetDeviceID(f.Fd())
		if err != nil {
			f.Close()
			continue
		}
		// Filter out printers that wouldn't understand the protocol.
		if !compatible(parseIEEE1284DeviceID(deviceID)) {
			f.Close()
			continue
		}
		return &Printer{File: f}, nil
	}
	return nil, nil
}

// Initialize initializes the printer for further operations.
func (p *Printer) Initialize() error {
	// Clear the print buffer.
	invalidate := make([]byte, 400)
	if _, err := p.File.Write(invalidate); err != nil {
		return err
	}

	// Initialize.
	if _, err := p.File.WriteString("\x1b\x40"); err != nil {
		return err
	}

	// Flush any former responses in the printer's queue.
	//
	// I'm not sure if this is necessary, or rather whether the kernel driver
	// does any buffering that could cause data to be returned at this point.
	/*
		for {
			dummy := make([]byte, 32)
			if _, err := f.Read(dummy); err == io.EOF {
				break
			}
		}
	*/

	return nil
}

// GetStatus retrieves the printer's status as raw data. The printer must be
// in an appropriate mode, i.e. on-line and not currently printing.
func (p *Printer) GetStatus() ([]byte, error) {
	// Request status information.
	if _, err := p.File.WriteString("\x1b\x69\x53"); err != nil {
		return nil, err
	}

	// We need to poll the device a bit.
	status := make([]byte, 32)
	start := time.Now()
	for {
		if n, err := p.File.Read(status); err == io.EOF {
			time.Sleep(10 * time.Millisecond)
		} else if err != nil {
			return nil, err
		} else if n < 32 {
			return nil, errors.New("invalid read")
		} else {
			return status, nil
		}
		if time.Now().Sub(start) > time.Second {
			return nil, errors.New("timeout")
		}
	}
}

// Close closes the underlying file.
func (p *Printer) Close() error {
	return p.File.Close()
}

// -----------------------------------------------------------------------------

type Status struct {
	errors []string
}

func decodeBitfieldErrors(b byte, errors [8]string) []string {
	var result []string
	for i := uint(0); i < 8; i++ {
		if b&(1<<i) != 0 {
			result = append(result, errors[i])
		}
	}
	return result
}

// TODO: What exactly do we need? Probably extend as needed.
func decodeStatusInformation(d []byte) Status {
	var status Status
	status.errors = append(status.errors, decodeBitfieldErrors(d[8], [8]string{
		"no media", "end of media", "cutter jam", "?", "printer in use",
		"printer turned off", "high-voltage adapter", "fan motor error"})...)
	status.errors = append(status.errors, decodeBitfieldErrors(d[9], [8]string{
		"replace media", "expansion buffer full", "communication error",
		"communication buffer full", "cover open", "cancel key",
		"media cannot be fed", "system error"})...)
	return status
}
