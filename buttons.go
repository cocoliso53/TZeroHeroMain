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
	buttonDebounce      = 30 * time.Millisecond
)

type buttonAction uint8

const (
	buttonConfirm buttonAction = iota
	buttonPrevious
	buttonNext
)

type piButtons struct {
	actions chan buttonAction
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
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

	buttons := &piButtons{
		actions: make(chan buttonAction, 8),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}

	go buttons.poll(pins)
	return buttons, nil
}

func (buttons *piButtons) poll(pins []debouncedPin) {
	defer close(buttons.done)
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

func (buttons *piButtons) Close() {
	buttons.once.Do(func() {
		close(buttons.stop)
		<-buttons.done
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
