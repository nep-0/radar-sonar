package repl

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
	// Raw REPL control sequences
	ctrlA = "\x01" // Enter raw REPL mode
	ctrlB = "\x02" // Exit raw REPL mode (back to normal REPL)
	ctrlC = "\x03" // Interrupt running code
	ctrlD = "\x04" // Execute in raw REPL / soft reset in normal REPL

	readBufSize = 1024

	DefaultBaud    = 115200
	DefaultTimeout = 5 * time.Second
)

// MicroPythonREPL manages a serial connection to a MicroPython device.
type MicroPythonREPL struct {
	port    serial.Port
	timeout time.Duration
	debug   bool
}

// New opens a serial port and returns a MicroPythonREPL instance.
func New(portName string, baud int, timeout time.Duration, debug bool) (*MicroPythonREPL, error) {
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

	return &MicroPythonREPL{
		port:    port,
		timeout: timeout,
		debug:   debug,
	}, nil
}

// Close closes the serial port.
func (m *MicroPythonREPL) Close() error {
	return m.port.Close()
}

// write sends bytes to the serial port.
func (m *MicroPythonREPL) write(data string) error {
	if m.debug {
		log.Printf("[TX] %q", data)
	}
	_, err := m.port.Write([]byte(data))
	return err
}

// readUntil reads from serial until the expected string is found or timeout.
func (m *MicroPythonREPL) readUntil(expected string, timeout time.Duration) (string, error) {
	var buf strings.Builder
	readBuf := make([]byte, readBufSize)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		n, err := m.port.Read(readBuf)
		if n > 0 {
			chunk := string(readBuf[:n])
			buf.WriteString(chunk)
			if m.debug {
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

// EnterRawREPL switches the device to raw REPL mode.
func (m *MicroPythonREPL) EnterRawREPL() error {
	// Interrupt any running code.
	if err := m.write(ctrlC); err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond)

	// Flush any pending data.
	m.port.ResetInputBuffer()

	// Enter raw REPL.
	if err := m.write(ctrlA); err != nil {
		return err
	}

	_, err := m.readUntil("raw REPL", m.timeout)
	if err != nil {
		return fmt.Errorf("failed to enter raw REPL: %w", err)
	}

	fmt.Println("[*] Entered raw REPL mode")
	return nil
}

// ExitRawREPL returns to the normal REPL.
func (m *MicroPythonREPL) ExitRawREPL() error {
	if err := m.write(ctrlB); err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond)
	fmt.Println("[*] Exited raw REPL mode")
	return nil
}

// ExecRaw executes a Python snippet in raw REPL mode and returns stdout/stderr.
func (m *MicroPythonREPL) ExecRaw(code string) (stdout string, stderr string, err error) {
	// MicroPython raw REPL expects code followed by Ctrl-D.
	// Response format: "OK" + <stdout> + "\x04" + <stderr> + "\x04" + ">".
	if err = m.write(code + ctrlD); err != nil {
		return
	}

	response, readErr := m.readUntil("\x04>", m.timeout)
	if readErr != nil {
		err = fmt.Errorf("exec read error: %w", readErr)
		return
	}

	// Some boards can emit prompt/newline bytes before OK; tolerate that.
	okIdx := strings.Index(response, "OK")
	if okIdx == -1 {
		err = fmt.Errorf("unexpected response prefix: %q", response)
		return
	}

	response = response[okIdx+len("OK"):]
	parts := strings.SplitN(response, "\x04", 3)
	if len(parts) < 2 {
		err = fmt.Errorf("malformed response: %q", response)
		return
	}

	stdout = parts[0]
	stderr = parts[1]
	return
}

// RunFile uploads and executes a local Python file on the device.
func (m *MicroPythonREPL) RunFile(filename string) (string, string, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return "", "", fmt.Errorf("failed to read file %s: %w", filename, err)
	}
	return m.ExecRaw(string(data))
}

// InteractiveLoop runs an interactive session with the raw REPL.
func (m *MicroPythonREPL) InteractiveLoop() {
	scanner := bufio.NewScanner(os.Stdin)
	replCompat := true
	fmt.Println("MicroPython Raw REPL Interactive Mode")
	fmt.Println("Commands: :quit, :file <path>, :reset, :raw [on|off], or enter Python code")
	fmt.Println("[*] REPL compatibility is ON (single-line expressions print results)")
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

		case line == ":raw":
			replCompat = !replCompat
			if replCompat {
				fmt.Println("[*] REPL compatibility ON")
			} else {
				fmt.Println("[*] REPL compatibility OFF (strict raw mode)")
			}

		case strings.HasPrefix(line, ":raw "):
			arg := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, ":raw ")))
			switch arg {
			case "on":
				replCompat = true
				fmt.Println("[*] REPL compatibility ON")
			case "off":
				replCompat = false
				fmt.Println("[*] REPL compatibility OFF (strict raw mode)")
			default:
				fmt.Println("[!] Usage: :raw [on|off]")
			}

		case line == ":reset":
			fmt.Println("[*] Soft resetting device...")
			if err := m.write(ctrlD); err != nil {
				fmt.Printf("[!] Reset error: %v\n", err)
			}
			time.Sleep(500 * time.Millisecond)
			if err := m.EnterRawREPL(); err != nil {
				fmt.Printf("[!] Failed to re-enter raw REPL: %v\n", err)
			}

		case strings.HasPrefix(line, ":file "):
			filename := strings.TrimSpace(strings.TrimPrefix(line, ":file "))
			fmt.Printf("[*] Uploading and running: %s\n", filename)
			stdout, stderr, err := m.RunFile(filename)
			printResult(stdout, stderr, err)

		case line == "":
			// Skip empty lines.

		default:
			code := collectCode(scanner, line)
			if replCompat {
				code = adaptInteractiveCode(code)
			}
			stdout, stderr, err := m.ExecRaw(code)
			printResult(stdout, stderr, err)
		}
	}
}

// collectCode collects potentially multi-line Python input.
func collectCode(scanner *bufio.Scanner, firstLine string) string {
	// If line ends with ":" it likely starts a block (if/for/def/etc.).
	if !strings.HasSuffix(strings.TrimSpace(firstLine), ":") {
		return firstLine
	}

	lines := []string{firstLine}
	fmt.Print("... ")
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		lines = append(lines, line)
		fmt.Print("... ")
	}

	return strings.Join(lines, "\n")
}

// adaptInteractiveCode emulates normal REPL behavior in raw REPL mode.
// Single-line expressions are evaluated and printed (if non-None), while
// statements still execute as-is.
func adaptInteractiveCode(code string) string {
	trimmed := strings.TrimSpace(code)
	if trimmed == "" || strings.Contains(code, "\n") || strings.HasSuffix(trimmed, ":") {
		return code
	}

	src := fmt.Sprintf("%q", code)
	return strings.Join([]string{
		"__copilot_src = " + src,
		"try:",
		"    __copilot_value = eval(__copilot_src)",
		"except SyntaxError:",
		"    exec(__copilot_src)",
		"else:",
		"    if __copilot_value is not None:",
		"        print(repr(__copilot_value))",
	}, "\n")
}

// printResult displays execution results.
func printResult(stdout, stderr string, err error) {
	if err != nil {
		fmt.Printf("[!] Error: %v\n", err)
		return
	}
	if stdout != "" {
		fmt.Print(stdout)
	}
	if stderr != "" {
		fmt.Printf("[stderr] %s\n", stderr)
	}
}
