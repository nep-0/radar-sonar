package main

import (
	"fmt"
	"log"

	"radar-sonar/ewm22a"
)

const (
	loRaPort      = "/dev/serial/by-id/usb-1a86_USB_Serial-if00-port0"
	loRaAddress   = 0x0002
	loRaChannel   = 20
	loRaNetworkID = 0
)

func main() {
	client, err := ewm22a.Open(loRaPort, ewm22a.DefaultOptions())
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			log.Printf("close error: %v", cerr)
		}
	}()

	if _, err := client.SetModeAndReopen(ewm22a.ModeConfig); err != nil {
		log.Fatalf("set config mode: %v", err)
	}
	if _, err := client.SetAddress(loRaAddress); err != nil {
		log.Fatalf("set address: %v", err)
	}
	if _, err := client.SetChannel(loRaChannel); err != nil {
		log.Fatalf("set channel: %v", err)
	}
	if _, err := client.SetNetworkID(loRaNetworkID); err != nil {
		log.Fatalf("set network ID: %v", err)
	}
	if _, err := client.SetModeAndReopen(ewm22a.ModeUARTLoRaBLE); err != nil {
		log.Fatalf("set transparent mode: %v", err)
	}

	fmt.Printf("configured LoRa: port=%s address=%d channel=%d network_id=%d\n", loRaPort, loRaAddress, loRaChannel, loRaNetworkID)
}
