package ewm22a

import (
	"errors"
	"io"
	"testing"
	"time"

	"go.bug.st/serial"
)

type fakePort struct {
	readChunks [][]byte
	writes     []string
	readIndex  int
}

func (p *fakePort) SetMode(*serial.Mode) error { return nil }

func (p *fakePort) Read(b []byte) (int, error) {
	if p.readIndex >= len(p.readChunks) {
		return 0, io.EOF
	}
	chunk := p.readChunks[p.readIndex]
	p.readIndex++
	copy(b, chunk)
	return len(chunk), nil
}

func (p *fakePort) Write(b []byte) (int, error) {
	p.writes = append(p.writes, string(b))
	return len(b), nil
}

func (p *fakePort) Drain() error             { return nil }
func (p *fakePort) ResetInputBuffer() error  { return nil }
func (p *fakePort) ResetOutputBuffer() error { return nil }
func (p *fakePort) SetDTR(bool) error        { return nil }
func (p *fakePort) SetRTS(bool) error        { return nil }
func (p *fakePort) GetModemStatusBits() (*serial.ModemStatusBits, error) {
	return &serial.ModemStatusBits{}, nil
}
func (p *fakePort) SetReadTimeout(time.Duration) error { return nil }
func (p *fakePort) Close() error                       { return nil }
func (p *fakePort) Break(time.Duration) error          { return nil }

func newFakeClient(chunks ...string) (*EWM22A, *fakePort) {
	port := &fakePort{}
	for _, chunk := range chunks {
		port.readChunks = append(port.readChunks, []byte(chunk))
	}
	return &EWM22A{port: port, timeout: 20 * time.Millisecond}, port
}

func TestCommandReturnsPayloadWithoutATOK(t *testing.T) {
	client, port := newFakeClient("DEVTYPE=EWM22A-900BWL22S\r\n", "AT_OK\r\n")

	got, err := client.Command("AT+DEVTYPE=?")
	if err != nil {
		t.Fatalf("Command returned error: %v", err)
	}
	if got != "DEVTYPE=EWM22A-900BWL22S" {
		t.Fatalf("payload = %q, want %q", got, "DEVTYPE=EWM22A-900BWL22S")
	}
	if len(port.writes) != 1 || port.writes[0] != "AT+DEVTYPE=?" {
		t.Fatalf("writes = %#v", port.writes)
	}
}

func TestCommandReturnsDocumentedATError(t *testing.T) {
	client, _ := newFakeClient("AT_PARAM_ERROR\r\n")

	got, err := client.Command("AT+CHANNEL=81")
	if got != "" {
		t.Fatalf("payload = %q, want empty", got)
	}
	var atErr *ATError
	if !errors.As(err, &atErr) {
		t.Fatalf("error = %T %v, want *ATError", err, err)
	}
	if atErr.Status != StatusParamError {
		t.Fatalf("status = %q, want %q", atErr.Status, StatusParamError)
	}
}

func TestCommandStripsEchoAndStatus(t *testing.T) {
	client, _ := newFakeClient("AT+HMODE=?\r\n1\r\nAT_OK\r\n")

	got, err := client.Command("AT+HMODE=?")
	if err != nil {
		t.Fatalf("Command returned error: %v", err)
	}
	if got != "1" {
		t.Fatalf("payload = %q, want %q", got, "1")
	}
}

func TestSetIntValidatesReturnedValue(t *testing.T) {
	client, _ := newFakeClient("18\r\nAT_OK\r\n")

	got, err := client.SetChannel(18)
	if err != nil {
		t.Fatalf("SetChannel returned error: %v", err)
	}
	if got != "18" {
		t.Fatalf("payload = %q, want %q", got, "18")
	}
}

func TestSetIntRejectsUnexpectedReturnedValue(t *testing.T) {
	client, _ := newFakeClient("17\r\nAT_OK\r\n")

	got, err := client.SetChannel(18)
	if err == nil {
		t.Fatal("SetChannel returned nil error")
	}
	if got != "17" {
		t.Fatalf("payload = %q, want %q", got, "17")
	}
}

func TestSetKeyAllowsDocumentedWriteOnlyResponse(t *testing.T) {
	client, _ := newFakeClient("0\r\nAT_OK\r\n")

	got, err := client.SetKey(1234)
	if err != nil {
		t.Fatalf("SetKey returned error: %v", err)
	}
	if got != "0" {
		t.Fatalf("payload = %q, want %q", got, "0")
	}
}
