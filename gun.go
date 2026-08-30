package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	gunTCPPort       = 4040
	gunSyncPort      = 4041
	syncAttempts     = 10
	syncInterval     = 100 * time.Millisecond
	syncReplyTimeout = 300 * time.Millisecond
)

var monotonicOrigin = time.Now()

type clockSample struct {
	roundTripUS int64
	offsetUS    float64
}

type gunT0Result struct {
	t0         time.Time
	connection net.Conn
	err        error
}

func monotonicMicroseconds() int64 {
	return time.Since(monotonicOrigin).Microseconds()
}

func synchronizeGun(gunIP net.IP) (clockSample, error) {
	connection, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		return clockSample{}, err
	}
	defer connection.Close()

	target := &net.UDPAddr{IP: gunIP, Port: gunSyncPort}
	var best *clockSample

	for sequence := 1; sequence <= syncAttempts; sequence++ {
		piSendUS := monotonicMicroseconds()
		request := fmt.Sprintf("SYNC %d %d\n", sequence, piSendUS)

		if _, err := connection.WriteToUDP([]byte(request), target); err != nil {
			log.Printf("SYNC sample=%d send failed: %v", sequence, err)
			continue
		}

		if err := connection.SetReadDeadline(time.Now().Add(syncReplyTimeout)); err != nil {
			return clockSample{}, err
		}

		buffer := make([]byte, 256)
		length, _, err := connection.ReadFromUDP(buffer)
		piReceiveUS := monotonicMicroseconds()
		if err != nil {
			log.Printf("SYNC sample=%d failed: %v", sequence, err)
			continue
		}

		fields := strings.Fields(string(buffer[:length]))
		if len(fields) != 5 || fields[0] != "SYNC_REPLY" {
			log.Printf("SYNC sample=%d invalid response", sequence)
			continue
		}

		replySequence, sequenceErr := strconv.Atoi(fields[1])
		echoedPiSendUS, echoErr := strconv.ParseInt(fields[2], 10, 64)
		gunReceiveUS, receiveErr := strconv.ParseInt(fields[3], 10, 64)
		gunSendUS, sendErr := strconv.ParseInt(fields[4], 10, 64)
		if sequenceErr != nil || echoErr != nil || receiveErr != nil || sendErr != nil ||
			replySequence != sequence || echoedPiSendUS != piSendUS {
			log.Printf("SYNC sample=%d malformed response", sequence)
			continue
		}

		roundTripUS := (piReceiveUS - piSendUS) - (gunSendUS - gunReceiveUS)
		if roundTripUS < 0 {
			log.Printf("SYNC sample=%d invalid RTT=%d", sequence, roundTripUS)
			continue
		}

		sample := clockSample{
			roundTripUS: roundTripUS,
			offsetUS:    float64((gunReceiveUS-piSendUS)+(gunSendUS-piReceiveUS)) / 2,
		}
		if best == nil || sample.roundTripUS < best.roundTripUS {
			selected := sample
			best = &selected
		}

		log.Printf(
			"SYNC sample=%d rtt_us=%d offset_us=%.1f",
			sequence,
			sample.roundTripUS,
			sample.offsetUS,
		)

		if sequence < syncAttempts {
			time.Sleep(syncInterval)
		}
	}

	if best == nil {
		return clockSample{}, fmt.Errorf("no valid clock samples")
	}

	log.Printf(
		"SYNC complete best_rtt_us=%d one_way_us=%d",
		best.roundTripUS,
		best.roundTripUS/2,
	)
	return *best, nil
}

func respondToHeartbeat(connection net.Conn, message string) error {
	_, err := fmt.Fprintf(
		connection,
		"PONG %s %d\n",
		strings.TrimPrefix(message, "PING "),
		time.Now().UnixNano(),
	)
	return err
}

func monitorGun(connection net.Conn, reader *bufio.Reader) {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			log.Printf("Gun disconnected: %v", err)
			return
		}

		message := strings.TrimSpace(line)
		if strings.HasPrefix(message, "PING ") {
			if err := respondToHeartbeat(connection, message); err != nil {
				log.Printf("Gun heartbeat response failed: %v", err)
				return
			}
		}
	}
}

func startGunCoordinator(piReady <-chan struct{}) (<-chan gunT0Result, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", gunTCPPort))
	if err != nil {
		return nil, err
	}

	results := make(chan gunT0Result, 1)
	go func() {
		defer listener.Close()
		t0, connection, err := waitForGunT0(listener, piReady)
		results <- gunT0Result{t0: t0, connection: connection, err: err}
	}()

	return results, nil
}

func waitForGunT0(listener net.Listener, piReady <-chan struct{}) (time.Time, net.Conn, error) {
	for {
		log.Printf("Waiting for gun on TCP port %d", gunTCPPort)
		connection, err := listener.Accept()
		if err != nil {
			return time.Time{}, nil, err
		}

		t0, err := handleGunSession(connection, piReady)
		if err != nil {
			log.Printf("Gun session failed: %v; waiting for reconnection", err)
			connection.Close()
			continue
		}

		return t0, connection, nil
	}
}

func handleGunSession(connection net.Conn, piReady <-chan struct{}) (time.Time, error) {
	remoteHost, _, err := net.SplitHostPort(connection.RemoteAddr().String())
	if err != nil {
		return time.Time{}, err
	}
	gunIP := net.ParseIP(remoteHost)
	reader := bufio.NewReader(connection)

	// HELLO means the gun has completed its own startup and is ready.
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return time.Time{}, err
		}

		message := strings.TrimSpace(line)
		log.Printf("Gun RX: %s", message)

		if strings.HasPrefix(message, "PING ") {
			if err := respondToHeartbeat(connection, message); err != nil {
				return time.Time{}, err
			}
			continue
		}

		if strings.HasPrefix(message, "HELLO ") {
			if _, err := fmt.Fprintln(connection, "WELCOME"); err != nil {
				return time.Time{}, err
			}
			log.Printf("Gun online; waiting for Pi camera calibration")
			break
		}
	}

	<-piReady
	log.Printf("Pi ready; requesting gun readiness")
	if _, err := fmt.Fprintln(connection, "PI_READY"); err != nil {
		return time.Time{}, err
	}

	if err := connection.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return time.Time{}, err
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return time.Time{}, err
		}

		message := strings.TrimSpace(line)
		log.Printf("Gun RX: %s", message)
		if strings.HasPrefix(message, "PING ") {
			if err := respondToHeartbeat(connection, message); err != nil {
				return time.Time{}, err
			}
			continue
		}
		if message == "GUN_READY" {
			break
		}
	}
	if err := connection.SetReadDeadline(time.Time{}); err != nil {
		return time.Time{}, err
	}
	log.Printf("Both devices ready; starting clock synchronization")

	best, err := synchronizeGun(gunIP)
	if err != nil {
		return time.Time{}, err
	}

	oneWayUS := best.roundTripUS / 2
	estimatedGunReceiveUTC := time.Now().UnixNano() + oneWayUS*1_000
	if _, err := fmt.Fprintf(
		connection,
		"CLOCK_SYNC %d %d\n",
		estimatedGunReceiveUTC,
		oneWayUS,
	); err != nil {
		return time.Time{}, err
	}
	log.Printf(
		"Clock reference sent utc_ns=%d one_way_us=%d",
		estimatedGunReceiveUTC,
		oneWayUS,
	)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return time.Time{}, err
		}

		message := strings.TrimSpace(line)
		log.Printf("Gun RX: %s", message)
		switch {
		case strings.HasPrefix(message, "PING "):
			if err := respondToHeartbeat(connection, message); err != nil {
				return time.Time{}, err
			}

		case strings.HasPrefix(message, "T0 "):
			t0Nanoseconds, err := strconv.ParseInt(strings.TrimSpace(message[3:]), 10, 64)
			if err != nil {
				fmt.Fprintln(connection, "CANCEL invalid-t0")
				continue
			}

			t0 := time.Unix(0, t0Nanoseconds)
			if time.Until(t0) <= 0 {
				fmt.Fprintln(connection, "CANCEL expired-t0")
				continue
			}

			if _, err := fmt.Fprintf(connection, "ACK_T0 %d\n", t0Nanoseconds); err != nil {
				return time.Time{}, err
			}

			log.Printf("T0 acknowledged: %s", t0.UTC().Format(time.RFC3339Nano))
			go monitorGun(connection, reader)
			return t0, nil
		}
	}
}
