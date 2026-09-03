#!/usr/bin/env python3

import argparse
import logging
import socket
import threading
import time
from collections import deque


SYNC_PORT = 4041
SYNC_INTERVAL_SECONDS = 1.0


def configure_logging(log_file: str) -> None:
    formatter = logging.Formatter("%(asctime)s %(message)s")
    handlers = [logging.StreamHandler()]

    if log_file:
        handlers.append(logging.FileHandler(log_file))

    logging.basicConfig(level=logging.INFO, format=formatter._fmt, handlers=handlers)


def synchronize_clock(gun_address: str, stop: threading.Event) -> None:
    samples: deque[tuple[int, float]] = deque(maxlen=20)
    sequence = 0

    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sync_socket:
        sync_socket.settimeout(0.5)

        while not stop.is_set():
            sequence += 1
            pi_send_us = time.monotonic_ns() // 1_000
            request = f"SYNC {sequence} {pi_send_us}\n"
            sync_socket.sendto(request.encode("ascii"), (gun_address, SYNC_PORT))

            try:
                response, _ = sync_socket.recvfrom(256)
                pi_receive_us = time.monotonic_ns() // 1_000
                fields = response.decode("ascii").strip().split()

                if len(fields) != 5 or fields[0] != "SYNC_REPLY":
                    raise ValueError("invalid response")

                reply_sequence, echoed_pi_send, gun_receive, gun_send = map(
                    int, fields[1:]
                )
                if reply_sequence != sequence or echoed_pi_send != pi_send_us:
                    raise ValueError("mismatched response")

                round_trip_us = (pi_receive_us - pi_send_us) - (
                    gun_send - gun_receive
                )
                offset_us = (
                    (gun_receive - pi_send_us) + (gun_send - pi_receive_us)
                ) / 2
                samples.append((round_trip_us, offset_us))
                best_rtt, best_offset = min(samples, key=lambda sample: sample[0])

                logging.info(
                    "SYNC sample=%d rtt_us=%d offset_us=%.1f "
                    "best_rtt_us=%d best_offset_us=%.1f",
                    sequence,
                    round_trip_us,
                    offset_us,
                    best_rtt,
                    best_offset,
                )
            except (OSError, UnicodeError, ValueError) as error:
                logging.warning("SYNC sample=%d failed: %s", sequence, error)

            stop.wait(SYNC_INTERVAL_SECONDS)


def handle_gun(connection: socket.socket, address: tuple[str, int]) -> None:
    logging.info("CONNECTED %s:%d", *address)
    stop_sync = threading.Event()
    sync_thread = threading.Thread(
        target=synchronize_clock, args=(address[0], stop_sync), daemon=True
    )
    sync_thread.start()

    try:
        with connection, connection.makefile(
            "r", encoding="utf-8", newline="\n"
        ) as stream:
            for raw_line in stream:
                message = raw_line.strip()
                if not message:
                    continue

                logging.info("RX %s:%d %s", *address, message)

                if message.startswith("HELLO "):
                    response = "WELCOME"
                elif message.startswith("PING "):
                    response = f"PONG {message[5:]} {time.time_ns()}"
                else:
                    response = "ERROR unknown-command"

                connection.sendall((response + "\n").encode("utf-8"))
                logging.info("TX %s:%d %s", *address, response)
    finally:
        stop_sync.set()
        sync_thread.join()

    logging.info("DISCONNECTED %s:%d", *address)


def main() -> None:
    parser = argparse.ArgumentParser(description="TZeroHero gun test server")
    parser.add_argument("--host", default="0.0.0.0")
    parser.add_argument("--port", type=int, default=4040)
    parser.add_argument("--log-file", default="gun-comms.log")
    args = parser.parse_args()

    configure_logging(args.log_file)

    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as server:
        server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        server.bind((args.host, args.port))
        server.listen()
        logging.info("LISTENING %s:%d", args.host, args.port)

        while True:
            connection, address = server.accept()
            try:
                handle_gun(connection, address)
            except (ConnectionError, OSError) as error:
                logging.warning("CONNECTION ERROR %s:%d %s", *address, error)


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        logging.info("STOPPED")
