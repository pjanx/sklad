package ql

import (
	"fmt"
	"io"
	"strings"
)

// Status is a decoder for the status packed returned by the printer.
type Status [32]byte

func (s *Status) MediaWidthMM() int  { return int(s[10]) }
func (s *Status) MediaLengthMM() int { return int(s[17]) }

type StatusType byte

const (
	StatusTypeReplyToRequest    StatusType = 0x00
	StatusTypePrintingCompleted            = 0x01
	StatusTypeErrorOccurred                = 0x02
	StatusTypeTurnedOff                    = 0x04
	StatusTypeNotification                 = 0x05
	StatusTypePhaseChange                  = 0x06
)

func (s *Status) Type() StatusType { return StatusType(s[18]) }

type StatusPhase byte

const (
	StatusPhaseReceiving StatusPhase = 0x00
	StatusPhasePrinting              = 0x01
)

func (s *Status) Phase() StatusPhase { return StatusPhase(s[19]) }

func decodeBitfieldErrors(b byte, errors [8]string) []string {
	var result []string
	for i := uint(0); i < 8; i++ {
		if b&(1<<i) != 0 {
			result = append(result, errors[i])
		}
	}
	return result
}

func (s *Status) Errors() (errors []string) {
	errors = append(errors, decodeBitfieldErrors(s[8], [8]string{
		"no media", "end of media", "cutter jam", "?", "printer in use",
		"printer turned off", "high-voltage adapter", "fan motor error"})...)
	errors = append(errors, decodeBitfieldErrors(s[9], [8]string{
		"replace media", "expansion buffer full", "communication error",
		"communication buffer full", "cover open", "cancel key",
		"media cannot be fed", "system error"})...)
	return
}

// -----------------------------------------------------------------------------

// String implements the Stringer interface.
func (s *Status) String() string {
	var b strings.Builder
	s.Dump(&b)
	return b.String()
}

// Dump writes the status data to an io.Writer in a human-readable format.
func (s *Status) Dump(f io.Writer) {
	/*
		if s[0] != 0x80 || s[1] != 0x20 || s[2] != 0x42 || s[3] != 0x34 {
			fmt.Fprintln(f, "unexpected status fixed bytes")
		}
	*/

	// Model code.
	switch m := s[4]; m {
	case 0x38:
		fmt.Fprintln(f, "model: QL-800")
	case 0x39:
		fmt.Fprintln(f, "model: QL-810W")
	case 0x41:
		fmt.Fprintln(f, "model: QL-820NWB")
	case 0x43:
		fmt.Fprintln(f, "model: QL-1100")
	case 0x44:
		fmt.Fprintln(f, "model: QL-1110NWB")
	case 0x45:
		fmt.Fprintln(f, "model: QL-1115NWB")
	default:
		fmt.Fprintln(f, "model:", m)
	}

	/*
		// s[6] seems to be 0x00 in a real-world QL-800, as in QL-1100 docs.
		if s[5] != 0x30 || s[6] != 0x30 || s[7] != 0x00 {
			fmt.Fprintln(f, "unexpected status fixed bytes")
		}
	*/

	// Error information 1.
	for _, e := range decodeBitfieldErrors(s[8], [8]string{
		"no media", "end of media", "cutter jam", "?", "printer in use",
		"printer turned off", "high-voltage adapter", "fan motor error"}) {
		fmt.Fprintln(f, "error 1:", e)
	}

	// Error information 2.
	for _, e := range decodeBitfieldErrors(s[9], [8]string{
		"replace media", "expansion buffer full", "communication error",
		"communication buffer full", "cover open", "cancel key",
		"media cannot be fed", "system error"}) {
		fmt.Fprintln(f, "error 2:", e)
	}

	// Media width.
	fmt.Fprintln(f, "media width:", s[10], "mm")

	// Media type.
	switch t := s[11]; t {
	case 0x00:
		fmt.Fprintln(f, "media: no media")
	case 0x4a, 0x0a: // 0x4a = J, in reality we get 0x0a, as in QL-1100 docs.
		fmt.Fprintln(f, "media: continuous length tape")
	case 0x4b, 0x0b: // 0x4b = K, in reality we get 0x0b, as in QL-1100 docs.
		fmt.Fprintln(f, "media: die-cut labels")
	default:
		fmt.Fprintln(f, "media:", t)
	}

	/*
		// In a real-world QL-800, s[14] seems to be:
		//  0x01 with die-cut 29mm long labels,
		//  0x14 with 29mm tape,
		//  0x23 with red-black 62mm tape,
		// and directly corresponds to physical pins on the tape.
		if s[12] != 0x00 || s[13] != 0x00 || s[14] != 0x3f {
			fmt.Fprintln(f, "unexpected status fixed bytes")
		}
	*/

	// Mode.
	fmt.Fprintln(f, "mode:", s[15])

	/*
		if s[16] != 0x00 {
			fmt.Fprintln(f, "unexpected status fixed bytes")
		}
	*/

	// Media length.
	fmt.Fprintln(f, "media length:", s[17], "mm")

	// Status type.
	switch t := s[18]; StatusType(t) {
	case StatusTypeReplyToRequest:
		fmt.Fprintln(f, "status type: reply to status request")
	case StatusTypePrintingCompleted:
		fmt.Fprintln(f, "status type: printing completed")
	case StatusTypeErrorOccurred:
		fmt.Fprintln(f, "status type: error occurred")
	case StatusTypeTurnedOff:
		fmt.Fprintln(f, "status type: turned off")
	case StatusTypeNotification:
		fmt.Fprintln(f, "status type: notification")
	case StatusTypePhaseChange:
		fmt.Fprintln(f, "status type: phase change")
	default:
		fmt.Fprintln(f, "status type:", t)
	}

	// Phase type.
	switch t := s[19]; StatusPhase(t) {
	case StatusPhaseReceiving:
		fmt.Fprintln(f, "phase state: receiving state")
	case StatusPhasePrinting:
		fmt.Fprintln(f, "phase state: printing state")
	default:
		fmt.Fprintln(f, "phase state:", t)
	}

	// Phase number.
	fmt.Fprintln(f, "phase number:", int(s[20])*256+int(s[21]))

	// Notification number.
	switch n := s[22]; n {
	case 0x00:
		fmt.Fprintln(f, "notification number: not available")
	case 0x03:
		fmt.Fprintln(f, "notification number: cooling (started)")
	case 0x04:
		fmt.Fprintln(f, "notification number: cooling (finished)")
	default:
		fmt.Fprintln(f, "notification number:", n)
	}

	/*
		// In a real-world QL-800, s[25] seems to be:
		//  0x01 with 29mm tape or die-cut 29mm long labels,
		//  0x81 with red-black 62mm tape.
		if s[23] != 0x00 || s[24] != 0x00 || s[25] != 0x00 || s[26] != 0x00 ||
			s[27] != 0x00 || s[28] != 0x00 || s[29] != 0x00 || s[30] != 0x00 ||
			s[31] != 0x00 {
			fmt.Fprintln(f, "unexpected status fixed bytes")
		}
	*/
}
