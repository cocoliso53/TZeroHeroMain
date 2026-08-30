# TZeroHeroMain

T-Zero Hero is a sprint training and timing system built around a Raspberry Pi 4 and an ESP32.

The system recreates a competitive race start, records the finish line at high frame rate, and calculates sprint time by comparing the selected finish frame timestamp against a shared start time, T0.

## Current scope

This repository contains the main Raspberry Pi application.

The first version focuses on:

* scheduling video capture relative to T0
* recording at 1280x720 and 100 fps
* saving per-frame camera metadata
* selecting a finish frame
* calculating sprint time from the frame wall-clock timestamp

ESP32 synchronization and UI will be handled separately as the project evolves.

## Gun communication test

Start the temporary TCP server on the Raspberry Pi:

```sh
python3 tools/gun_server.py
```

It listens on port `4040`, prints messages to the terminal, and writes them to
`gun-comms.log`. Stop it with `Ctrl+C`.

## Stack

* Go
* Raspberry Pi OS Lite
* rpicam-apps
* Raspberry Pi Camera Module 3
