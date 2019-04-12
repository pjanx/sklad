package main

import (
	"log"

	"janouch.name/sklad/ql"
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
	case 0x4a, 0x0a: // 0x4a = J, in reality we get 0x0a as in QL-1100 docs
		log.Println("media: continuous length tape")
	case 0x4b, 0x0b: // 0x4b = K, in reality we get 0x0b as in QL-1100 docs
		log.Println("media: die-cut labels")
	default:
		log.Println("media:", b)
	}

	// In a real-world QL-800, d[14] seems to be:
	//  0x01 with die-cut 29mm long labels,
	//  0x14 with 29mm tape,
	//  0x23 with red-black 62mm tape,
	// and directly corresponds to physical pins on the tape.
	if d[12] != 0x00 || d[13] != 0x00 || d[14] != 0x3f {
		log.Println("unexpected status fixed bytes")
	}

	// Mode.
	log.Println("mode:", d[15])

	if d[16] != 0x00 {
		log.Println("unexpected status fixed bytes")
	}

	// Media length.
	log.Println("media length:", d[17], "mm")

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

	// In a real-world QL-800, d[25] seems to be:
	//  0x01 with 29mm tape or die-cut 29mm long labels,
	//  0x81 with red-black 62mm tape.
	if d[23] != 0x00 || d[24] != 0x00 || d[25] != 0x00 || d[26] != 0x00 ||
		d[27] != 0x00 || d[28] != 0x00 || d[29] != 0x00 || d[30] != 0x00 ||
		d[31] != 0x00 {
		log.Println("unexpected status fixed bytes")
	}
}

func main() {
	printer, err := ql.Open()
	if err != nil {
		log.Fatalln(err)
	}
	if printer == nil {
		log.Fatalln("no suitable printer found")
	}
	defer printer.Close()

	if err := printer.Initialize(); err != nil {
		log.Fatalln(err)
	}

	status, err := printer.GetStatus()
	if err != nil {
		log.Fatalln(err)
	}

	printStatusInformation(status)
}
