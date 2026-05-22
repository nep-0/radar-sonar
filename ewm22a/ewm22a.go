package ewm22a

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"go.bug.st/serial"
)

const (
	readBufSize = 1024

	DefaultBaud       = 115200
	DefaultTimeout    = 5 * time.Second
	DefaultRebootWait = 2 * time.Second

	ModeConfig       = 0
	ModeUARTLoRaBLE  = 1
	ModeUARTLoRaWiFi = 4

	TransmissionTransparent = 0
	TransmissionFixed       = 1
)

// Options configures an EWM22A serial connection.
type Options struct {
	Baud       int
	Timeout    time.Duration
	RebootWait time.Duration
	Debug      bool
}

// DefaultOptions returns the module's documented default UART settings.
func DefaultOptions() Options {
	return Options{
		Baud:       DefaultBaud,
		Timeout:    DefaultTimeout,
		RebootWait: DefaultRebootWait,
	}
}

// EWM22A manages a serial connection to an EWM22A-400/900BWL22S module.
type EWM22A struct {
	portName   string
	baud       int
	port       serial.Port
	timeout    time.Duration
	rebootWait time.Duration
	debug      bool
}

// New opens a serial port and returns an EWM22A instance.
func New(portName string, baud int, timeout time.Duration, debug bool) (*EWM22A, error) {
	opts := DefaultOptions()
	opts.Baud = baud
	opts.Timeout = timeout
	opts.Debug = debug
	return Open(portName, opts)
}

// Open opens a serial port and returns an EWM22A instance.
func Open(portName string, opts Options) (*EWM22A, error) {
	opts = normalizeOptions(opts)
	mode := &serial.Mode{
		BaudRate: opts.Baud,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}

	port, err := serial.Open(portName, mode)
	if err != nil {
		return nil, fmt.Errorf("failed to open serial port %s: %w", portName, err)
	}

	if err := port.SetReadTimeout(opts.Timeout); err != nil {
		port.Close()
		return nil, fmt.Errorf("failed to set read timeout: %w", err)
	}

	return &EWM22A{
		portName:   portName,
		baud:       opts.Baud,
		port:       port,
		timeout:    opts.Timeout,
		rebootWait: opts.RebootWait,
		debug:      opts.Debug,
	}, nil
}

// OpenLoRaTransparent opens the module and switches it to UART-LoRa transparent mode.
func OpenLoRaTransparent(portName string, opts Options) (*EWM22A, error) {
	client, err := Open(portName, opts)
	if err != nil {
		return nil, err
	}
	if _, err := client.SetModeAndReopen(ModeUARTLoRaBLE); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

// OpenConfig opens the module and switches it to UART AT configuration mode.
func OpenConfig(portName string, opts Options) (*EWM22A, error) {
	client, err := Open(portName, opts)
	if err != nil {
		return nil, err
	}
	if _, err := client.SetModeAndReopen(ModeConfig); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

func normalizeOptions(opts Options) Options {
	if opts.Baud == 0 {
		opts.Baud = DefaultBaud
	}
	if opts.Timeout == 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.RebootWait == 0 {
		opts.RebootWait = DefaultRebootWait
	}
	return opts
}

// Close closes the serial port.
func (e *EWM22A) Close() error {
	return e.port.Close()
}

// Reopen closes and opens the same serial port with the same settings.
func (e *EWM22A) Reopen() error {
	if err := e.port.Close(); err != nil {
		return err
	}

	mode := &serial.Mode{
		BaudRate: e.baud,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}

	port, err := serial.Open(e.portName, mode)
	if err != nil {
		return fmt.Errorf("failed to reopen serial port %s: %w", e.portName, err)
	}
	if err := port.SetReadTimeout(e.timeout); err != nil {
		port.Close()
		return fmt.Errorf("failed to set read timeout: %w", err)
	}

	e.port = port
	return nil
}

// write sends bytes to the serial port.
func (e *EWM22A) write(data []byte) error {
	if e.debug {
		log.Printf("[TX] %x", data)
	}
	_, err := e.port.Write(data)
	return err
}

// Write sends data through the module in transparent mode.
func (e *EWM22A) Write(data []byte) error {
	return e.write(data)
}

// WriteString sends string data through the module in transparent mode.
func (e *EWM22A) WriteString(data string) error {
	return e.write([]byte(data))
}

// readUntil reads from serial until the expected string is found or timeout.
func (e *EWM22A) readUntil(expected string, timeout time.Duration) (string, error) {
	var buf strings.Builder
	readBuf := make([]byte, readBufSize)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		n, err := e.port.Read(readBuf)
		if n > 0 {
			chunk := string(readBuf[:n])
			buf.WriteString(chunk)
			if e.debug {
				log.Printf("[RX] %q", chunk)
			}
			if strings.Contains(buf.String(), expected) {
				return buf.String(), nil
			}
		}
		if err != nil {
			if err == io.EOF {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			return buf.String(), fmt.Errorf("read error: %w", err)
		}
	}

	return buf.String(), fmt.Errorf("timeout waiting for %q, got: %q", expected, buf.String())
}

// ReadUntil waits for the given marker and returns the accumulated text.
func (e *EWM22A) ReadUntil(expected string) (string, error) {
	return e.readUntil(expected, e.timeout)
}

// Command sends one UART AT command. EWM22A UART AT commands do not include CRLF.
func (e *EWM22A) Command(command string) (string, error) {
	if strings.ContainsAny(command, "\r\n\t ") {
		return "", fmt.Errorf("AT command contains unsupported whitespace: %q", command)
	}
	if err := e.WriteString(command); err != nil {
		return "", err
	}
	return e.ReadUntil("AT_OK")
}

// SetMode sets the module working mode. The module reboots after this command.
func (e *EWM22A) SetMode(mode int) (string, error) {
	return e.Command(fmt.Sprintf("AT+HMODE=%d", mode))
}

// SetModeAndReopen sets the module mode, waits for reboot, and reopens the port.
func (e *EWM22A) SetModeAndReopen(mode int) (string, error) {
	response, err := e.SetMode(mode)
	if err != nil {
		return response, err
	}
	time.Sleep(e.rebootWait)
	if err := e.Reopen(); err != nil {
		return response, err
	}
	return response, nil
}

// Query reads an AT setting, for example Query("ADDR") sends AT+ADDR=?.
func (e *EWM22A) Query(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "\r\n\t +=?") {
		return "", fmt.Errorf("invalid AT setting name: %q", name)
	}
	return e.Command("AT+" + strings.ToUpper(name) + "=?")
}

// SetAddress sets the LoRa module address, 0..65535.
func (e *EWM22A) SetAddress(address int) (string, error) {
	if err := checkRange("address", address, 0, 65535); err != nil {
		return "", err
	}
	return e.setInt("ADDR", address)
}

// SetChannel sets the LoRa channel. EWM22A-900BWL22S supports 0..80.
func (e *EWM22A) SetChannel(channel int) (string, error) {
	if err := checkRange("channel", channel, 0, 80); err != nil {
		return "", err
	}
	return e.setInt("CHANNEL", channel)
}

// SetNetworkID sets the LoRa network ID, 0..255.
func (e *EWM22A) SetNetworkID(networkID int) (string, error) {
	if err := checkRange("network ID", networkID, 0, 255); err != nil {
		return "", err
	}
	return e.setInt("NETID", networkID)
}

// SetKey sets the LoRa encryption key, 0..65535.
func (e *EWM22A) SetKey(key int) (string, error) {
	if err := checkRange("key", key, 0, 65535); err != nil {
		return "", err
	}
	return e.setInt("KEY", key)
}

// SetRate sets the LoRa air-rate index, 0..7.
func (e *EWM22A) SetRate(rate int) (string, error) {
	if err := checkRange("rate", rate, 0, 7); err != nil {
		return "", err
	}
	return e.setInt("RATE", rate)
}

// SetPacketLength sets the packet length index: 0=240, 1=128, 2=64, 3=32.
func (e *EWM22A) SetPacketLength(packet int) (string, error) {
	if err := checkRange("packet", packet, 0, 3); err != nil {
		return "", err
	}
	return e.setInt("PACKET", packet)
}

// SetPower sets the LoRa power index, 0..3.
func (e *EWM22A) SetPower(power int) (string, error) {
	if err := checkRange("power", power, 0, 3); err != nil {
		return "", err
	}
	return e.setInt("POWER", power)
}

// SetTransmissionMode sets LoRa transmission mode: 0=transparent, 1=fixed.
func (e *EWM22A) SetTransmissionMode(mode int) (string, error) {
	if err := checkRange("transmission mode", mode, 0, 1); err != nil {
		return "", err
	}
	return e.setInt("TRANS", mode)
}

// SetRouter enables or disables LoRa relay mode.
func (e *EWM22A) SetRouter(enabled bool) (string, error) {
	return e.setBool("ROUTER", enabled)
}

// SetLBT enables or disables Listen Before Talk.
func (e *EWM22A) SetLBT(enabled bool) (string, error) {
	return e.setBool("LBT", enabled)
}

// SetEnvironmentRSSI enables or disables environment RSSI reads.
func (e *EWM22A) SetEnvironmentRSSI(enabled bool) (string, error) {
	return e.setBool("ERSSI", enabled)
}

// SetDataRSSI enables or disables appending RSSI to received UART data.
func (e *EWM22A) SetDataRSSI(enabled bool) (string, error) {
	return e.setBool("DRSSI", enabled)
}

// SetWORRole sets the WOR role, only meaningful in mode 7: 0=receiver, 1=sender.
func (e *EWM22A) SetWORRole(role int) (string, error) {
	if err := checkRange("WOR role", role, 0, 1); err != nil {
		return "", err
	}
	return e.setInt("WOR", role)
}

// SetWORPeriod sets the WOR period index, 0..7.
func (e *EWM22A) SetWORPeriod(period int) (string, error) {
	if err := checkRange("WOR period", period, 0, 7); err != nil {
		return "", err
	}
	return e.setInt("WTIME", period)
}

// SetDelay sets WOR delayed sleep in milliseconds, 0..65535.
func (e *EWM22A) SetDelay(delayMS int) (string, error) {
	if err := checkRange("delay", delayMS, 0, 65535); err != nil {
		return "", err
	}
	return e.setInt("DELAY", delayMS)
}

func (e *EWM22A) setInt(name string, value int) (string, error) {
	return e.Command(fmt.Sprintf("AT+%s=%d", name, value))
}

func (e *EWM22A) setBool(name string, enabled bool) (string, error) {
	value := 0
	if enabled {
		value = 1
	}
	return e.setInt(name, value)
}

func checkRange(name string, value, minValue, maxValue int) error {
	if value < minValue || value > maxValue {
		return fmt.Errorf("%s must be %d..%d, got %d", name, minValue, maxValue, value)
	}
	return nil
}

// ReadFor reads whatever arrives for up to the given duration.
func (e *EWM22A) ReadFor(timeout time.Duration) (string, error) {
	var buf strings.Builder
	readBuf := make([]byte, readBufSize)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		n, err := e.port.Read(readBuf)
		if n > 0 {
			chunk := string(readBuf[:n])
			buf.WriteString(chunk)
			if e.debug {
				log.Printf("[RX] %q", chunk)
			}
		}
		if err != nil {
			if err == io.EOF {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			return buf.String(), fmt.Errorf("read error: %w", err)
		}
		if n == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}

	return buf.String(), nil
}

// Send transmits the provided payload as raw bytes.
func (e *EWM22A) Send(payload []byte) error {
	return e.Write(payload)
}

// SendString transmits the provided payload as a string.
func (e *EWM22A) SendString(payload string) error {
	return e.WriteString(payload)
}

// SendFile transmits a local file as raw bytes.
func (e *EWM22A) SendFile(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", filename, err)
	}
	return e.Send(data)
}

// InteractiveLoop runs a simple serial console for sending data.
func (e *EWM22A) InteractiveLoop() {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("EWM22A serial console")
	fmt.Println("Commands: :quit, :file <path>, or enter text to send")
	fmt.Println(strings.Repeat("-", 50))

	for {
		fmt.Print(">>> ")
		if !scanner.Scan() {
			break
		}

		line := scanner.Text()
		switch {
		case line == ":quit" || line == ":exit":
			fmt.Println("Exiting...")
			return

		case strings.HasPrefix(line, ":file "):
			filename := strings.TrimSpace(strings.TrimPrefix(line, ":file "))
			if err := e.SendFile(filename); err != nil {
				fmt.Printf("[!] Error: %v\n", err)
			}

		case line == "":
			// Skip empty lines.

		default:
			if err := e.SendString(line); err != nil {
				fmt.Printf("[!] Error: %v\n", err)
			}
		}
	}
}
