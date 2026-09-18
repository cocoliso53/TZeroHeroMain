package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/stianeikeland/go-rpio/v4"
)

const (
	newSprintButtonGPIO = 17
	previousButtonGPIO  = 27
	nextButtonGPIO      = 22
	syncStatusLEDGPIO   = 23
	buttonDebounce      = 30 * time.Millisecond
)

type syncLEDCommand uint8

const (
	syncLEDSearching syncLEDCommand = iota
	syncLEDSuccess
)

type buttonAction uint8

const (
	buttonConfirm buttonAction = iota
	buttonPrevious
	buttonNext
)

type piButtons struct {
	actions     chan buttonAction
	ledCommands chan syncLEDCommand
	stop        chan struct{}
	waitGroup   sync.WaitGroup
	once        sync.Once
}

type debouncedPin struct {
	pin       rpio.Pin
	action    buttonAction
	lastRead  rpio.State
	stable    rpio.State
	changedAt time.Time
}

func startPiButtons() (*piButtons, error) {
	if err := rpio.Open(); err != nil {
		return nil, fmt.Errorf("open Raspberry Pi GPIO: %w", err)
	}

	pins := []debouncedPin{
		{pin: rpio.Pin(newSprintButtonGPIO), action: buttonConfirm},
		{pin: rpio.Pin(previousButtonGPIO), action: buttonPrevious},
		{pin: rpio.Pin(nextButtonGPIO), action: buttonNext},
	}
	for index := range pins {
		pins[index].pin.Input()
		pins[index].pin.PullUp()
		pins[index].lastRead = pins[index].pin.Read()
		pins[index].stable = pins[index].lastRead
		pins[index].changedAt = time.Now()
	}
	statusLED := rpio.Pin(syncStatusLEDGPIO)
	statusLED.Output()
	statusLED.Low()

	buttons := &piButtons{
		actions:     make(chan buttonAction, 8),
		ledCommands: make(chan syncLEDCommand, 4),
		stop:        make(chan struct{}),
	}

	buttons.waitGroup.Add(2)
	go buttons.poll(pins)
	go buttons.runStatusLED(statusLED)
	buttons.setSyncSearching()
	return buttons, nil
}

func (buttons *piButtons) poll(pins []debouncedPin) {
	defer buttons.waitGroup.Done()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case now := <-ticker.C:
			for index := range pins {
				reading := pins[index].pin.Read()
				if reading != pins[index].lastRead {
					pins[index].lastRead = reading
					pins[index].changedAt = now
				}

				if reading == pins[index].stable ||
					now.Sub(pins[index].changedAt) < buttonDebounce {
					continue
				}

				pins[index].stable = reading
				if reading == rpio.Low {
					select {
					case buttons.actions <- pins[index].action:
					default:
					}
				}
			}

		case <-buttons.stop:
			return
		}
	}
}

func (buttons *piButtons) runStatusLED(pin rpio.Pin) {
	defer buttons.waitGroup.Done()
	defer pin.Low()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	mode := syncLEDSearching
	ledOn := false
	nextChange := time.Now()
	successTransitionsRemaining := 0

	setLED := func(on bool) {
		ledOn = on
		if on {
			pin.High()
		} else {
			pin.Low()
		}
	}

	for {
		select {
		case command := <-buttons.ledCommands:
			mode = command
			setLED(true)
			if command == syncLEDSuccess {
				successTransitionsRemaining = 5
				nextChange = time.Now().Add(120 * time.Millisecond)
			} else {
				nextChange = time.Now().Add(150 * time.Millisecond)
			}

		case now := <-ticker.C:
			if now.Before(nextChange) {
				continue
			}

			switch mode {
			case syncLEDSearching:
				setLED(!ledOn)
				if ledOn {
					nextChange = now.Add(150 * time.Millisecond)
				} else {
					nextChange = now.Add(1850 * time.Millisecond)
				}

			case syncLEDSuccess:
				if successTransitionsRemaining == 0 {
					continue
				}
				setLED(!ledOn)
				successTransitionsRemaining--
				nextChange = now.Add(120 * time.Millisecond)
			}

		case <-buttons.stop:
			return
		}
	}
}

func (buttons *piButtons) setLEDCommand(command syncLEDCommand) {
	if buttons == nil {
		return
	}
	select {
	case buttons.ledCommands <- command:
	case <-buttons.stop:
	}
}

func (buttons *piButtons) setSyncSearching() {
	buttons.setLEDCommand(syncLEDSearching)
}

func (buttons *piButtons) showSyncSuccess() {
	buttons.setLEDCommand(syncLEDSuccess)
}

func (buttons *piButtons) Close() {
	buttons.once.Do(func() {
		close(buttons.stop)
		buttons.waitGroup.Wait()
		rpio.Close()
	})
}

func drainButtonActions(actions <-chan buttonAction) {
	for actions != nil {
		select {
		case <-actions:
		default:
			return
		}
	}
}
