#!/usr/bin/env python3

import argparse
import logging
import socket
import time


def configure_logging(log_file: str) -> None:
    formatter = logging.Formatter("%(asctime)s %(message)s")
    handlers = [logging.StreamHandler()]

    if log_file:
        handlers.append(logging.FileHandler(log_file))

    logging.basicConfig(level=logging.INFO, format=formatter._fmt, handlers=handlers)


def handle_gun(connection: socket.socket, address: tuple[str, int]) -> None:
    logging.info("CONNECTED %s:%d", *address)

    with connection, connection.makefile("r", encoding="utf-8", newline="\n") as stream:
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
