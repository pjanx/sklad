package ql

import (
	"errors"
	"image"
	"io"
	"os"
	"path/filepath"
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

type Printer struct {
	File         *os.File
	Manufacturer string
	Model        string

	LastStatus *Status
	MediaInfo  *MediaInfo

	// StatusNotify is called whenever we receive a status packet.
	StatusNotify func(*Status)
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
		parsedID := parseIEEE1284DeviceID(deviceID)
		// Filter out printers that wouldn't understand the protocol.
		if !compatible(parsedID) {
			f.Close()
			continue
		}
		return &Printer{
			File:         f,
			Manufacturer: parsedID.FindFirst("MANUFACTURER", "MFG"),
			Model:        parsedID.FindFirst("MODEL", "MDL"),
		}, nil
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
	// I haven't checked if this is the kernel driver or the printer doing
	// the buffering that causes data to be returned at this point.
	var dummy [32]byte
	for {
		if _, err := p.File.Read(dummy[:]); err == io.EOF {
			break
		}
	}

	return nil
}

var errTimeout = errors.New("timeout")
var errInvalidRead = errors.New("invalid read")

func (p *Printer) updateStatus(status Status) {
	p.LastStatus = &status
	if p.StatusNotify != nil {
		p.StatusNotify(p.LastStatus)
	}
}

// pollStatusBytes waits for the printer to send a status packet and returns
// it as raw data.
func (p *Printer) pollStatusBytes(
	timeout time.Duration) (*Status, error) {
	start, buf := time.Now(), [32]byte{}
	for {
		if n, err := p.File.Read(buf[:]); err == io.EOF {
			time.Sleep(10 * time.Millisecond)
		} else if err != nil {
			return nil, err
		} else if n < 32 {
			return nil, errInvalidRead
		} else {
			p.updateStatus(Status(buf))
			return p.LastStatus, nil
		}
		if time.Now().Sub(start) > timeout {
			return nil, errTimeout
		}
	}
}

// Request new status information from the printer. The printer
// must be in an appropriate mode, i.e. on-line and not currently printing.
func (p *Printer) UpdateStatus() error {
	// Request status information.
	if _, err := p.File.WriteString("\x1b\x69\x53"); err != nil {
		return err
	}

	// Retrieve status information.
	if _, err := p.pollStatusBytes(time.Second); err != nil {
		p.LastStatus = nil
		return err
	}
	return nil
}

var errErrorOccurred = errors.New("error occurred")
var errUnexpectedStatus = errors.New("unexpected status")
var errUnknownMedia = errors.New("unknown media")

func (p *Printer) Print(image image.Image) error {
	data := makePrintData(p.LastStatus, image)
	if data == nil {
		return errUnknownMedia
	}
	if _, err := p.File.Write(data); err != nil {
		return err
	}

	// See diagrams: we may receive an error status instead of the transition
	// to the printing state. Or even after it.
	//
	// Not sure how exactly cooling behaves and I don't want to test it.
	for {
		status, err := p.pollStatusBytes(10 * time.Second)
		if err != nil {
			return err
		}

		switch status.Type() {
		case StatusTypePhaseChange:
			// Nothing to do.
		case StatusTypePrintingCompleted:
			return nil
		case StatusTypeErrorOccurred:
			return errErrorOccurred
		default:
			return errUnexpectedStatus
		}
	}
}

// Close closes the underlying file.
func (p *Printer) Close() error {
	return p.File.Close()
}
