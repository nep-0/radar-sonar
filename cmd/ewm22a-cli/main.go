package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"radar-sonar/ewm22a"
)

func main() {
	var (
		port       = flag.String("port", "", "serial port, for example COM3 or /dev/ttyUSB0")
		baud       = flag.Int("baud", ewm22a.DefaultBaud, "baud rate")
		timeout    = flag.Duration("timeout", ewm22a.DefaultTimeout, "serial read timeout")
		wait       = flag.Duration("wait", 300*time.Millisecond, "how long to wait for a response after each send")
		rebootWait = flag.Duration("reboot-wait", 2*time.Second, "how long to wait after a mode change before reopening the port")
		modeFlag   = flag.String("mode", "", "optional startup mode: config or lora")
		debug      = flag.Bool("debug", false, "log raw TX/RX bytes")
	)
	flag.Parse()

	if *port == "" {
		log.Fatal("missing -port")
	}

	opts := ewm22a.DefaultOptions()
	opts.Baud = *baud
	opts.Timeout = *timeout
	opts.RebootWait = *rebootWait
	opts.Debug = *debug

	client, err := ewm22a.Open(*port, opts)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			log.Printf("close error: %v", cerr)
		}
	}()

	mode := "at"
	switch strings.ToLower(strings.TrimSpace(*modeFlag)) {
	case "":
	case "config":
		if err := setMode(client, ewm22a.ModeConfig); err != nil {
			log.Fatal(err)
		}
		mode = "at"
	case "lora":
		if err := setMode(client, ewm22a.ModeUARTLoRaBLE); err != nil {
			log.Fatal(err)
		}
		mode = "data"
	default:
		log.Fatalf("unknown -mode %q; use config or lora", *modeFlag)
	}

	fmt.Println("EWM22A interactive CLI")
	fmt.Println("Commands: :quit, :exit, :help, :mode, :config, :transparent, :at <cmd>, :send <data>, :file <path>, :read, :hex <bytes>")
	fmt.Println("Plain text is only accepted in AT mode when it starts with AT.")
	fmt.Println(strings.Repeat("-", 60))

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Printf("[%s] >>> ", mode)
		if !scanner.Scan() {
			return
		}

		line := scanner.Text()
		switch {
		case line == ":quit" || line == ":exit":
			return

		case line == ":help":
			printHelp()

		case line == ":mode":
			fmt.Println(mode)

		case line == ":config":
			if err := setMode(client, ewm22a.ModeConfig); err != nil {
				fmt.Printf("[!] config error: %v\n", err)
				continue
			}
			mode = "at"

		case line == ":transparent":
			if err := setMode(client, ewm22a.ModeUARTLoRaBLE); err != nil {
				fmt.Printf("[!] transparent error: %v\n", err)
				continue
			}
			mode = "data"

		case strings.HasPrefix(line, ":at "):
			cmd := strings.TrimSpace(strings.TrimPrefix(line, ":at "))
			if cmd == "" {
				fmt.Println("[!] missing AT command")
				continue
			}
			if err := sendAT(client, cmd, *wait); err != nil {
				fmt.Printf("[!] AT error: %v\n", err)
			}

		case strings.HasPrefix(line, ":send "):
			payload := strings.TrimPrefix(line, ":send ")
			if err := client.SendString(payload); err != nil {
				fmt.Printf("[!] send error: %v\n", err)
				continue
			}
			printReply(client, *wait)

		case line == ":read":
			out, err := client.ReadFor(*wait)
			if err != nil {
				fmt.Printf("[!] read error: %v\n", err)
				continue
			}
			if out != "" {
				fmt.Print(out)
			}

		case strings.HasPrefix(line, ":file "):
			filename := strings.TrimSpace(strings.TrimPrefix(line, ":file "))
			if err := client.SendFile(filename); err != nil {
				fmt.Printf("[!] send error: %v\n", err)
				continue
			}
			printReply(client, *wait)

		case strings.HasPrefix(line, ":hex "):
			payload, err := parseHex(strings.TrimSpace(strings.TrimPrefix(line, ":hex ")))
			if err != nil {
				fmt.Printf("[!] parse error: %v\n", err)
				continue
			}
			if err := client.Send(payload); err != nil {
				fmt.Printf("[!] send error: %v\n", err)
				continue
			}
			printReply(client, *wait)

		case line == "":
			// Skip empty lines.

		default:
			if mode == "at" {
				upper := strings.ToUpper(strings.TrimSpace(line))
				if !strings.HasPrefix(upper, "AT") {
					fmt.Println("[!] use :send for raw payloads, or switch to :transparent")
					continue
				}
				if err := sendAT(client, line, *wait); err != nil {
					fmt.Printf("[!] AT error: %v\n", err)
				}
				continue
			}
			if err := client.SendString(line); err != nil {
				fmt.Printf("[!] send error: %v\n", err)
				continue
			}
			printReply(client, *wait)
		}
	}
}

func printHelp() {
	fmt.Println(":config       send AT+HMODE=0, wait for reboot, reopen port, stay in AT mode")
	fmt.Println(":transparent  send AT+HMODE=1, wait for reboot, reopen port, switch to data mode")
	fmt.Println(":at <cmd>     send one AT command")
	fmt.Println(":send <data>  send raw payload")
	fmt.Println(":file <path>  send file bytes")
	fmt.Println(":hex <hex>    send raw hex bytes")
	fmt.Println(":read         read pending response")
}

func setMode(client *ewm22a.EWM22A, mode int) error {
	response, err := client.SetModeAndReopen(mode)
	if err != nil {
		return err
	}
	if response != "" {
		fmt.Print(response)
	}
	return nil
}

func sendAT(client *ewm22a.EWM22A, cmd string, wait time.Duration) error {
	if err := client.SendString(cmd); err != nil {
		return err
	}
	printReply(client, wait)
	return nil
}

func printReply(client *ewm22a.EWM22A, wait time.Duration) {
	out, err := client.ReadFor(wait)
	if err != nil {
		fmt.Printf("[!] read error: %v\n", err)
		return
	}
	if out != "" {
		fmt.Print(out)
	}
}

func parseHex(s string) ([]byte, error) {
	s = strings.ReplaceAll(s, " ", "")
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("hex string must have even length")
	}

	out := make([]byte, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		var b byte
		if _, err := fmt.Sscanf(s[i:i+2], "%02x", &b); err != nil {
			return nil, err
		}
		out[i/2] = b
	}
	return out, nil
}
