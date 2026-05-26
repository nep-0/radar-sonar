package mr20

import (
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"time"

	"go.bug.st/serial"
)

const (
	header0 = 0xAA
	header1 = 0xAA
	tail0   = 0x55
	tail1   = 0x55

	FrameLen    = 14
	PayloadLen  = 8
	readBufSize = 128

	DefaultBaud    = 115200
	DefaultTimeout = 5 * time.Second

	MessageRadarState    = 0x0201
	MessageRegionState   = 0x0404
	MessageHeartbeat     = 0x0700
	MessageObjectStatus  = 0x060A
	MessageObjectGeneral = 0x060B
)

var (
	ErrShortFrame  = errors.New("short MR20 frame")
	ErrBadHeader   = errors.New("bad MR20 frame header")
	ErrBadTrailer  = errors.New("bad MR20 frame trailer")
	ErrReadTimeout = errors.New("timeout waiting for MR20 frame")
)

// Frame is one MR20 serial frame.
type Frame struct {
	MessageID uint16
	Payload   [PayloadLen]byte
}

// ObjectStatus is message 0x60A.
type ObjectStatus struct {
	Count            int
	MeasurementCount uint16
	InterfaceVersion int
}

// Target is message 0x60B.
type Target struct {
	ID                      int
	LongitudinalDistanceM   float64
	LateralDistanceM        float64
	LongitudinalVelocityMPS float64
	LateralVelocityMPS      float64
	DynamicProperty         int
	RCSDBM2                 float64
	RangeM                  float64
	AngleRad                float64
}

// Heartbeat is message 0x700.
type Heartbeat struct {
	Major int
	Minor int
	Patch int
}

// Parser incrementally parses MR20 serial bytes.
type Parser struct {
	buf []byte
}

// NewParser creates an empty MR20 parser.
func NewParser() *Parser {
	return &Parser{}
}

// Push appends bytes and returns all complete frames that can be parsed.
func (p *Parser) Push(data []byte) ([]Frame, error) {
	p.buf = append(p.buf, data...)

	var frames []Frame
	for {
		if len(p.buf) < 2 {
			return frames, nil
		}

		headerAt := findHeader(p.buf)
		if headerAt == -1 {
			keep := min(len(p.buf), 1)
			p.buf = append([]byte(nil), p.buf[len(p.buf)-keep:]...)
			return frames, nil
		}
		if headerAt > 0 {
			p.buf = p.buf[headerAt:]
		}
		if len(p.buf) < FrameLen {
			return frames, nil
		}
		if p.buf[FrameLen-2] != tail0 || p.buf[FrameLen-1] != tail1 {
			p.buf = p.buf[1:]
			continue
		}

		frame, err := ParseFrame(p.buf[:FrameLen])
		if err != nil {
			return frames, err
		}
		frames = append(frames, frame)
		p.buf = p.buf[FrameLen:]
	}
}

// BuildFrame serializes one MR20 frame.
func BuildFrame(messageID uint16, payload [PayloadLen]byte) []byte {
	out := make([]byte, FrameLen)
	out[0] = header0
	out[1] = header1
	out[2] = byte(messageID)
	out[3] = byte(messageID >> 8)
	copy(out[4:12], payload[:])
	out[12] = tail0
	out[13] = tail1
	return out
}

// ParseFrame parses one complete MR20 frame.
func ParseFrame(data []byte) (Frame, error) {
	if len(data) < FrameLen {
		return Frame{}, ErrShortFrame
	}
	if data[0] != header0 || data[1] != header1 {
		return Frame{}, ErrBadHeader
	}
	if data[12] != tail0 || data[13] != tail1 {
		return Frame{}, ErrBadTrailer
	}

	var payload [PayloadLen]byte
	copy(payload[:], data[4:12])
	return Frame{
		MessageID: uint16(data[2]) | uint16(data[3])<<8,
		Payload:   payload,
	}, nil
}

// ObjectStatus decodes message 0x60A.
func (f Frame) ObjectStatus() (ObjectStatus, bool) {
	if f.MessageID != MessageObjectStatus {
		return ObjectStatus{}, false
	}

	return ObjectStatus{
		Count:            int(f.Payload[0]),
		MeasurementCount: uint16(f.Payload[2]) | uint16(f.Payload[3])<<8,
		InterfaceVersion: int((f.Payload[3] >> 4) & 0x0F),
	}, true
}

// Target decodes message 0x60B.
func (f Frame) Target() (Target, bool) {
	if f.MessageID != MessageObjectGeneral {
		return Target{}, false
	}

	p := f.Payload
	longM := (float64(int(p[1])*32+int(p[2]>>3)) * 0.1) - 500
	latM := (float64(int(p[2]&0x07)*256+int(p[3])) * 0.1) - 102.3
	longVel := (float64(int(p[4])<<2+int(p[5]>>6)) * 0.25) - 128
	latVel := (float64(int(p[5]&0x3F)*8+int(p[6]>>5)) * 0.25) - 64

	return Target{
		ID:                      int(p[0]),
		LongitudinalDistanceM:   longM,
		LateralDistanceM:        latM,
		LongitudinalVelocityMPS: longVel,
		LateralVelocityMPS:      latVel,
		DynamicProperty:         int(p[6] & 0x07),
		RCSDBM2:                 (float64(p[7]) * 0.5) - 64,
		RangeM:                  math.Hypot(longM, latM),
		AngleRad:                math.Atan2(latM, longM),
	}, true
}

// Heartbeat decodes message 0x700.
func (f Frame) Heartbeat() (Heartbeat, bool) {
	if f.MessageID != MessageHeartbeat {
		return Heartbeat{}, false
	}
	return Heartbeat{
		Major: int(f.Payload[0]),
		Minor: int(f.Payload[1]),
		Patch: int(f.Payload[2]),
	}, true
}

// MR20 manages a serial connection to an MR20 radar.
type MR20 struct {
	port    serial.Port
	timeout time.Duration
	debug   bool
	parser  *Parser
}

// New opens a serial port and returns an MR20 instance.
func New(portName string, baud int, timeout time.Duration, debug bool) (*MR20, error) {
	mode := &serial.Mode{
		BaudRate: baud,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}

	port, err := serial.Open(portName, mode)
	if err != nil {
		return nil, fmt.Errorf("failed to open serial port %s: %w", portName, err)
	}
	if err := port.SetReadTimeout(timeout); err != nil {
		port.Close()
		return nil, fmt.Errorf("failed to set read timeout: %w", err)
	}

	return &MR20{
		port:    port,
		timeout: timeout,
		debug:   debug,
		parser:  NewParser(),
	}, nil
}

// Close closes the serial port.
func (m *MR20) Close() error {
	return m.port.Close()
}

// WriteFrame writes one MR20 command/config frame.
func (m *MR20) WriteFrame(messageID uint16, payload [PayloadLen]byte) error {
	frame := BuildFrame(messageID, payload)
	if m.debug {
		log.Printf("[TX] %x", frame)
	}
	_, err := m.port.Write(frame)
	return err
}

// ReadFrame reads until one complete MR20 frame is parsed or timeout expires.
func (m *MR20) ReadFrame() (Frame, error) {
	readBuf := make([]byte, readBufSize)
	deadline := time.Now().Add(m.timeout)

	for time.Now().Before(deadline) {
		n, err := m.port.Read(readBuf)
		if n > 0 {
			chunk := readBuf[:n]
			if m.debug {
				log.Printf("[RX] %x", chunk)
			}
			frames, parseErr := m.parser.Push(chunk)
			if parseErr != nil {
				return Frame{}, parseErr
			}
			if len(frames) > 0 {
				return frames[0], nil
			}
		}
		if err != nil {
			if err == io.EOF {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			return Frame{}, fmt.Errorf("read error: %w", err)
		}
	}

	return Frame{}, ErrReadTimeout
}

func findHeader(data []byte) int {
	for i := 0; i+1 < len(data); i++ {
		if data[i] == header0 && data[i+1] == header1 {
			return i
		}
	}
	return -1
}
