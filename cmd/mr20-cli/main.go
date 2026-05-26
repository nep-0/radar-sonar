package main

import (
	"flag"
	"fmt"
	"log"

	"radar-sonar/mr20"
)

func main() {
	var (
		port    = flag.String("port", "", "serial port, for example COM3 or /dev/ttyUSB0")
		baud    = flag.Int("baud", mr20.DefaultBaud, "baud rate")
		timeout = flag.Duration("timeout", mr20.DefaultTimeout, "serial read timeout")
		debug   = flag.Bool("debug", false, "log raw RX bytes")
		raw     = flag.Bool("raw", false, "print raw frame payloads")
	)
	flag.Parse()

	if *port == "" {
		log.Fatal("missing -port")
	}

	radar, err := mr20.New(*port, *baud, *timeout, *debug)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if cerr := radar.Close(); cerr != nil {
			log.Printf("close error: %v", cerr)
		}
	}()

	log.Printf("reading MR20 frames from %s at %d baud", *port, *baud)
	for {
		frame, err := radar.ReadFrame()
		if err != nil {
			log.Printf("read error: %v", err)
			continue
		}

		printFrame(frame, *raw)
	}
}

func printFrame(frame mr20.Frame, raw bool) {
	if raw {
		fmt.Printf("id=%#04x payload=% x\n", frame.MessageID, frame.Payload)
	}

	if status, ok := frame.ObjectStatus(); ok {
		fmt.Printf("objects count=%d meas=%d iface=%d\n",
			status.Count,
			status.MeasurementCount,
			status.InterfaceVersion,
		)
		return
	}

	if target, ok := frame.Target(); ok {
		fmt.Printf(
			"target id=%d long=%.1fm lat=%.1fm vlong=%.2fm/s vlat=%.2fm/s dyn=%d rcs=%.1fdB range=%.2fm angle=%.3frad\n",
			target.ID,
			target.LongitudinalDistanceM,
			target.LateralDistanceM,
			target.LongitudinalVelocityMPS,
			target.LateralVelocityMPS,
			target.DynamicProperty,
			target.RCSDBM2,
			target.RangeM,
			target.AngleRad,
		)
		return
	}

	if heartbeat, ok := frame.Heartbeat(); ok {
		fmt.Printf("heartbeat version=%d.%d.%d\n", heartbeat.Major, heartbeat.Minor, heartbeat.Patch)
		return
	}

	if !raw {
		fmt.Printf("id=%#04x payload=% x\n", frame.MessageID, frame.Payload)
	}
}
