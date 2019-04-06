package main

import (
	"io"
	"log"
	"os"
	"time"
)

func decodeBitfieldErrors(b byte, errors [8]string) []string {
	var result []string
	for i := uint(0); i < 8; i++ {
		if b&(1<<i) != 0 {
			result = append(result, errors[i])
		}
	}
	return result
}

// -----------------------------------------------------------------------------

type brotherStatus struct {
	errors []string
}

// TODO: What exactly do we need? Probably extend as needed.
func decodeStatusInformation(d []byte) brotherStatus {
	var status brotherStatus
	status.errors = append(status.errors, decodeBitfieldErrors(d[8], [8]string{
		"no media", "end of media", "cutter jam", "?", "printer in use",
		"printer turned off", "high-voltage adapter", "fan motor error"})...)
	status.errors = append(status.errors, decodeBitfieldErrors(d[9], [8]string{
		"replace media", "expansion buffer full", "communication error",
		"communication buffer full", "cover open", "cancel key",
		"media cannot be fed", "system error"})...)
	return status
}

// -----------------------------------------------------------------------------

func printStatusInformation(d []byte) {
	if d[0] != 0x80 || d[1] != 0x20 || d[2] != 0x42 || d[3] != 0x34 {
		log.Println("unexpected status fixed bytes")
	}

	// Model code.
	switch b := d[4]; b {
	case 0x38:
		log.Println("model: QL-800")
	case 0x39:
		log.Println("model: QL-810W")
	case 0x41:
		log.Println("model: QL-820NWB")
	default:
		log.Println("model:", b)
	}

	// d[6] seems to be 0x00 in a real-world QL-800.
	if d[5] != 0x30 || d[6] != 0x30 || d[7] != 0x00 {
		log.Println("unexpected status fixed bytes")
	}

	// Error information 1.
	for _, e := range decodeBitfieldErrors(d[8], [8]string{
		"no media", "end of media", "cutter jam", "?", "printer in use",
		"printer turned off", "high-voltage adapter", "fan motor error"}) {
		log.Println("error:", e)
	}

	// Error information 2.
	for _, e := range decodeBitfieldErrors(d[9], [8]string{
		"replace media", "expansion buffer full", "communication error",
		"communication buffer full", "cover open", "cancel key",
		"media cannot be fed", "system error"}) {
		log.Println("error:", e)
	}

	// Media width.
	log.Println("media width:", d[10], "mm")

	// Media type.
	switch b := d[11]; b {
	case 0x00:
		log.Println("media: no media")
	case 0x4a:
		log.Println("media: continuous length tape")
	case 0x4b:
		log.Println("media: die-cut labels")
	default:
		log.Println("media:", b)
	}

	// d[14] seems to be 0x14 in a real-world QL-800.
	if d[12] != 0x00 || d[13] != 0x00 || d[14] != 0x3f {
		log.Println("unexpected status fixed bytes")
	}

	// Mode.
	log.Println("mode:", d[15])

	if d[16] != 0x00 {
		log.Println("unexpected status fixed bytes")
	}

	// Media length.
	log.Println("media width:", d[17], "mm")

	// Status type.
	switch b := d[18]; b {
	case 0x00:
		log.Println("status type: reply to status request")
	case 0x01:
		log.Println("status type: printing completed")
	case 0x02:
		log.Println("status type: error occurred")
	case 0x04:
		log.Println("status type: turned off")
	case 0x05:
		log.Println("status type: notification")
	case 0x06:
		log.Println("status type: phase change")
	default:
		log.Println("status type:", b)
	}

	// Phase type.
	switch b := d[19]; b {
	case 0x00:
		log.Println("phase state: receiving state")
	case 0x01:
		log.Println("phase state: printing state")
	default:
		log.Println("phase state:", b)
	}

	// Phase number.
	log.Println("phase number:", int(d[20])*256+int(d[21]))

	// Notification number.
	switch b := d[22]; b {
	case 0x00:
		log.Println("notification number: not available")
	case 0x03:
		log.Println("notification number: cooling (started)")
	case 0x04:
		log.Println("notification number: cooling (finished)")
	default:
		log.Println("notification number:", b)
	}

	// d[25] seems to be 0x01 in a real-world QL-800.
	if d[23] != 0x00 || d[24] != 0x00 || d[25] != 0x00 || d[26] != 0x00 ||
		d[27] != 0x00 || d[28] != 0x00 || d[29] != 0x00 || d[30] != 0x00 ||
		d[31] != 0x00 {
		log.Println("unexpected status fixed bytes")
	}
}

func main() {
	// Linux usblp module, located in /drivers/usb/class/usblp.c
	// (at least that's where the trails go, I don't understand the code)
	f, err := os.OpenFile("/dev/usb/lp0", os.O_RDWR, 0)
	if err != nil {
		log.Fatalln(err)
	}
	defer f.Close()

	// Flush any former responses in the printer's queue.
	for {
		dummy := make([]byte, 32)
		if _, err := f.Read(dummy); err == io.EOF {
			break
		}
	}

	// Clear the print buffer.
	invalidate := make([]byte, 400)
	if _, err := f.Write(invalidate); err != nil {
		log.Fatalln(err)
	}

	// Initialize.
	if _, err := f.WriteString("\x1b\x40"); err != nil {
		log.Fatalln(err)
	}

	// Request status information.
	if _, err := f.WriteString("\x1b\x69\x53"); err != nil {
		log.Fatalln(err)
	}

	// We need to poll the device.
	status := make([]byte, 32)
	for {
		if n, err := f.Read(status); err == io.EOF {
			time.Sleep(10 * time.Millisecond)
		} else if err != nil {
			log.Fatalln(err)
		} else if n < 32 {
			log.Fatalln("invalid read")
		} else {
			break
		}
	}

	printStatusInformation(status)
}
