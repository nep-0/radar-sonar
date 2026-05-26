package mr20

import (
	"math"
	"testing"
)

func TestParseTargetFrame(t *testing.T) {
	frameBytes := []byte{
		0xAA, 0xAA,
		0x0B, 0x06,
		0x57, 0x9D, 0x34, 0x1D, 0x47, 0xA0, 0x02, 0x00,
		0x55, 0x55,
	}

	frame, err := ParseFrame(frameBytes)
	if err != nil {
		t.Fatal(err)
	}
	if frame.MessageID != MessageObjectGeneral {
		t.Fatalf("message id = %#x", frame.MessageID)
	}

	target, ok := frame.Target()
	if !ok {
		t.Fatal("target decode failed")
	}
	if target.ID != 0x57 {
		t.Fatalf("target id = %d", target.ID)
	}
	assertClose(t, target.LongitudinalDistanceM, 3)
	assertClose(t, target.LateralDistanceM, 3)
	assertClose(t, target.LongitudinalVelocityMPS, -56.5)
	assertClose(t, target.LateralVelocityMPS, 0)
	if target.DynamicProperty != 2 {
		t.Fatalf("dynamic property = %d", target.DynamicProperty)
	}
}

func TestParserResync(t *testing.T) {
	parser := NewParser()
	frames, err := parser.Push([]byte{0x00, 0xAA})
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 0 {
		t.Fatalf("frames = %d", len(frames))
	}

	frames, err = parser.Push([]byte{
		0xAA, 0x0A, 0x06,
		0x03, 0x00, 0x12, 0x34, 0x00, 0x00, 0x00, 0x00,
		0x55, 0x55,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 {
		t.Fatalf("frames = %d", len(frames))
	}
	status, ok := frames[0].ObjectStatus()
	if !ok {
		t.Fatal("status decode failed")
	}
	if status.Count != 3 {
		t.Fatalf("count = %d", status.Count)
	}
}

func TestBuildFrame(t *testing.T) {
	var payload [PayloadLen]byte
	payload[0] = 0x81
	frame := BuildFrame(0x0500, payload)
	if len(frame) != FrameLen {
		t.Fatalf("len = %d", len(frame))
	}
	if frame[0] != 0xAA || frame[1] != 0xAA || frame[2] != 0x00 || frame[3] != 0x05 {
		t.Fatalf("bad prefix/id: %x", frame[:4])
	}
	if frame[12] != 0x55 || frame[13] != 0x55 {
		t.Fatalf("bad trailer: %x", frame[12:])
	}
}

func assertClose(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %v, want %v", got, want)
	}
}
