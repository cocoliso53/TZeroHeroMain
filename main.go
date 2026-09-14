package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/veandco/go-sdl2/img"
	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
)

type FrameMetadata struct {
	FrameWallClock int64 `json:"FrameWallClock"`
}

type sprintMode struct {
	name              string
	captureStartDelay time.Duration
	recordingDuration time.Duration
}

var sprintModes = []sprintMode{
	{name: "Short / Accel", captureStartDelay: 2 * time.Second, recordingDuration: 4 * time.Second},
	{name: "60 m", captureStartDelay: 6 * time.Second, recordingDuration: 4 * time.Second},
	{name: "100 m", captureStartDelay: 9 * time.Second, recordingDuration: 7 * time.Second},
	{name: "150 m", captureStartDelay: 14 * time.Second, recordingDuration: 10 * time.Second},
	{name: "200 m", captureStartDelay: 19 * time.Second, recordingDuration: 13 * time.Second},
}

func record(duration time.Duration) error {
	cmd := exec.Command(
		"rpicam-vid",
		"-t", strconv.FormatInt(duration.Milliseconds(), 10),
		"--width", "1280",
		"--height", "720",
		"--framerate", "100",
		"--level", "4.2",
		"--denoise", "cdn_off",
		"-n",
		"--metadata", "metadata.json",
		"--metadata-format", "json",
		"-o", "video.h264",
	)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

func loadMetadata(path string) ([]FrameMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var frames []FrameMetadata
	if err := json.Unmarshal(data, &frames); err != nil {
		return nil, err
	}

	return frames, nil
}

func extractFrame(index int) error {
	filter := fmt.Sprintf("select=eq(n\\,%d)", index)

	cmd := exec.Command(
		"ffmpeg",
		"-y",
		"-i", "video.h264",
		"-vf", filter,
		"-frames:v", "1",
		"current-frame.jpg",
	)

	cmd.Stdout = nil
	cmd.Stderr = nil

	return cmd.Run()
}

func drawFinishLine(
	renderer *sdl.Renderer,
	outW int32,
	outH int32,
) error {
	lineX := outW / 2

	if err := renderer.SetDrawColor(255, 0, 0, 255); err != nil {
		return err
	}

	for offset := int32(-1); offset <= 1; offset++ {
		if err := renderer.DrawLine(
			lineX+offset,
			0,
			lineX+offset,
			outH,
		); err != nil {
			return err
		}
	}

	return nil
}

func changeSprintMode(index *int, delta int) {
	*index = (*index + delta + len(sprintModes)) % len(sprintModes)
	mode := sprintModes[*index]
	windowEnd := mode.captureStartDelay + mode.recordingDuration
	fmt.Printf("Mode: %s (%.0f-%.0f seconds)\n", mode.name,
		mode.captureStartDelay.Seconds(), windowEnd.Seconds())
}

func drawSprintMode(
	renderer *sdl.Renderer,
	font *ttf.Font,
	outW int32,
	mode sprintMode,
) error {
	windowEnd := mode.captureStartDelay + mode.recordingDuration
	label := fmt.Sprintf("%s   %.0f-%.0f s", mode.name,
		mode.captureStartDelay.Seconds(), windowEnd.Seconds())
	surface, err := font.RenderUTF8Blended(label,
		sdl.Color{R: 255, G: 255, B: 255, A: 255})
	if err != nil {
		return err
	}
	defer surface.Free()

	texture, err := renderer.CreateTextureFromSurface(surface)
	if err != nil {
		return err
	}
	defer texture.Destroy()

	textRect := sdl.Rect{
		X: (outW - surface.W) / 2,
		Y: 20,
		W: surface.W,
		H: surface.H,
	}
	background := sdl.Rect{
		X: textRect.X - 12,
		Y: textRect.Y - 8,
		W: textRect.W + 24,
		H: textRect.H + 16,
	}
	if err := renderer.SetDrawColor(0, 0, 0, 255); err != nil {
		return err
	}
	if err := renderer.FillRect(&background); err != nil {
		return err
	}
	return renderer.Copy(texture, nil, &textRect)
}

func readJPEG(reader *bufio.Reader) ([]byte, error) {
	var frame []byte

	for {
		b, err := reader.ReadByte()
		if err != nil {
			return nil, err
		}

		if b != 0xFF {
			continue
		}

		next, err := reader.ReadByte()
		if err != nil {
			return nil, err
		}

		if next == 0xD8 {
			frame = append(frame, 0xFF, 0xD8)
			break
		}
	}

	for {
		b, err := reader.ReadByte()
		if err != nil {
			return nil, err
		}

		frame = append(frame, b)

		if len(frame) >= 2 &&
			frame[len(frame)-2] == 0xFF &&
			frame[len(frame)-1] == 0xD9 {
			return frame, nil
		}
	}
}

func textureFromJPEG(
	renderer *sdl.Renderer,
	data []byte,
) (*sdl.Texture, error) {
	rw, err := sdl.RWFromMem(data)
	if err != nil {
		return nil, err
	}

	return img.LoadTextureRW(renderer, rw, true)
}

func readTerminalCommands() <-chan string {
	commands := make(chan string)
	go func() {
		defer close(commands)
		reader := bufio.NewReader(os.Stdin)
		for {
			text, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			commands <- strings.TrimSpace(text)
		}
	}()
	return commands
}

var errQuitRequested = errors.New("quit requested")

func waitForT0WithPreview(
	renderer *sdl.Renderer,
	font *ttf.Font,
	outW int32,
	outH int32,
	commands <-chan string,
	buttonActions <-chan buttonAction,
	coordinator *gunCoordinator,
	modeIndex *int,
) (time.Time, error) {
	fmt.Println("Live calibration mode; waiting for gun T0")
	changeSprintMode(modeIndex, 0)
	fmt.Print("[s]tart [n]ext mode [p]revious mode [q]uit > ")

	cmd := exec.Command(
		"rpicam-vid",
		"-t", "0",
		"--width", "1280",
		"--height", "720",
		"--framerate", "30",
		"--codec", "mjpeg",
		"-n",
		"-o", "-",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return time.Time{}, err
	}

	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return time.Time{}, err
	}

	coordinator.setAccepting(true)
	t0Accepted := false
	defer func() {
		if !t0Accepted {
			coordinator.setAccepting(false)
		}
	}()
	requestGunT0 := func() {
		if err := coordinator.requestT0(); err != nil {
			fmt.Printf("Cannot request T0: %v\n", err)
			return
		}
		fmt.Println("T0 requested from gun")
	}

	frames := make(chan []byte, 1)

	go func() {
		defer close(frames)

		reader := bufio.NewReaderSize(stdout, 1024*1024)

		for {
			frame, err := readJPEG(reader)
			if err != nil {
				if err != io.EOF {
					fmt.Printf("preview read error: %v\n", err)
				}
				return
			}

			select {
			case frames <- frame:
			default:
				select {
				case <-frames:
				default:
				}

				select {
				case frames <- frame:
				default:
				}
			}
		}
	}()

	var texture *sdl.Texture

	stopCamera := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}

		_ = cmd.Wait()
	}

	defer func() {
		if texture != nil {
			texture.Destroy()
		}
	}()

	for {
		select {
		case t0 := <-coordinator.t0s:
			t0Accepted = true
			fmt.Printf("T0 accepted for %s: %s\n", sprintModes[*modeIndex].name,
				t0.UTC().Format(time.RFC3339Nano))
			stopCamera()
			if texture != nil {
				texture.Destroy()
				texture = nil
			}
			renderer.SetDrawColor(0, 0, 0, 255)
			renderer.Clear()
			renderer.Present()
			return t0, nil

		case command, ok := <-commands:
			if !ok {
				commands = nil
				break
			}

			switch command {
			case "s":
				requestGunT0()
			case "n":
				changeSprintMode(modeIndex, 1)
			case "p":
				changeSprintMode(modeIndex, -1)
			case "q":
				stopCamera()
				return time.Time{}, errQuitRequested

			default:
				fmt.Print("[s]tart [n]ext mode [p]revious mode [q]uit > ")
			}

		case action := <-buttonActions:
			switch action {
			case buttonConfirm:
				requestGunT0()
			case buttonPrevious:
				changeSprintMode(modeIndex, -1)
			case buttonNext:
				changeSprintMode(modeIndex, 1)
			}

		default:
		}

		// Keep SDL responsive too.
		for event := sdl.PollEvent(); event != nil; event = sdl.PollEvent() {
			switch event.(type) {
			case *sdl.QuitEvent:
				stopCamera()
				return time.Time{}, errQuitRequested
			}
		}

		select {
		case frame, ok := <-frames:
			if !ok {
				return time.Time{}, fmt.Errorf("camera preview stopped unexpectedly")
			}

			newTexture, err := textureFromJPEG(renderer, frame)
			if err != nil {
				return time.Time{}, err
			}

			if texture != nil {
				texture.Destroy()
			}

			texture = newTexture

		default:
		}

		renderer.SetDrawColor(0, 0, 0, 255)
		renderer.Clear()

		if texture != nil {
			dst := sdl.Rect{
				X: 0,
				Y: 0,
				W: outW,
				H: outH,
			}

			if err := renderer.Copy(texture, nil, &dst); err != nil {
				return time.Time{}, err
			}
		}

		if err := drawFinishLine(renderer, outW, outH); err != nil {
			return time.Time{}, err
		}
		if err := drawSprintMode(renderer, font, outW, sprintModes[*modeIndex]); err != nil {
			return time.Time{}, err
		}

		renderer.Present()

		sdl.Delay(16)
	}
}

func displayFrame(
	renderer *sdl.Renderer,
	font *ttf.Font,
	outW int32,
	outH int32,
	elapsed time.Duration,
) error {
	texture, err := img.LoadTexture(renderer, "current-frame.jpg")
	if err != nil {
		return err
	}
	defer texture.Destroy()

	dst := sdl.Rect{
		X: 0,
		Y: 0,
		W: outW,
		H: outH,
	}

	renderer.SetDrawColor(0, 0, 0, 255)
	renderer.Clear()

	if err := renderer.Copy(texture, nil, &dst); err != nil {
		return err
	}

	if err := drawFinishLine(renderer, outW, outH); err != nil {
		return err
	}

	label := fmt.Sprintf("%.3f s", elapsed.Seconds())

	surface, err := font.RenderUTF8Blended(
		label,
		sdl.Color{
			R: 255,
			G: 255,
			B: 255,
			A: 255,
		},
	)
	if err != nil {
		return err
	}
	defer surface.Free()

	textTexture, err := renderer.CreateTextureFromSurface(surface)
	if err != nil {
		return err
	}
	defer textTexture.Destroy()

	textRect := sdl.Rect{
		X: 30,
		Y: 30,
		W: surface.W,
		H: surface.H,
	}

	if err := renderer.Copy(textTexture, nil, &textRect); err != nil {
		return err
	}

	renderer.Present()

	return nil
}

func displayBlack(renderer *sdl.Renderer) error {
	if err := renderer.SetDrawColor(0, 0, 0, 255); err != nil {
		return err
	}
	if err := renderer.Clear(); err != nil {
		return err
	}
	renderer.Present()
	return nil
}

func displayStopwatch(
	renderer *sdl.Renderer,
	font *ttf.Font,
	outW int32,
	outH int32,
	elapsed time.Duration,
) error {
	var label string
	if elapsed < 0 {
		label = fmt.Sprintf("START IN %.1f", (-elapsed).Seconds())
	} else {
		label = fmt.Sprintf("%.3f", elapsed.Seconds())
	}

	surface, err := font.RenderUTF8Blended(label, sdl.Color{R: 255, G: 255, B: 255, A: 255})
	if err != nil {
		return err
	}
	defer surface.Free()

	textTexture, err := renderer.CreateTextureFromSurface(surface)
	if err != nil {
		return err
	}
	defer textTexture.Destroy()

	if err := renderer.SetDrawColor(0, 0, 0, 255); err != nil {
		return err
	}
	if err := renderer.Clear(); err != nil {
		return err
	}

	textRect := sdl.Rect{
		X: (outW - surface.W) / 2,
		Y: (outH - surface.H) / 2,
		W: surface.W,
		H: surface.H,
	}
	if err := renderer.Copy(textTexture, nil, &textRect); err != nil {
		return err
	}
	renderer.Present()
	return nil
}

func recordWithStopwatch(
	renderer *sdl.Renderer,
	font *ttf.Font,
	outW int32,
	outH int32,
	t0 time.Time,
	coordinator *gunCoordinator,
	mode sprintMode,
) (time.Time, error) {
	defer coordinator.setAccepting(false)

	captureStart := t0.Add(mode.captureStartDelay)
	fmt.Printf("%s capture starts: %s (duration %.0f s)\n", mode.name,
		captureStart.UTC().Format(time.RFC3339Nano), mode.recordingDuration.Seconds())

	recordingDone := make(chan error, 1)
	captureTimer := time.NewTimer(max(time.Until(captureStart), 0))
	defer captureTimer.Stop()
	recordingStarted := false

	ticker := time.NewTicker(16 * time.Millisecond)
	defer ticker.Stop()
	t0Logged := false
	applyReplacement := func(replacementT0 time.Time) {
		t0 = replacementT0
		captureStart = t0.Add(mode.captureStartDelay)
		t0Logged = false
		if !captureTimer.Stop() {
			select {
			case <-captureTimer.C:
			default:
			}
		}
		captureTimer.Reset(max(time.Until(captureStart), 0))
		fmt.Printf("T0 replaced: %s\n", t0.UTC().Format(time.RFC3339Nano))
		fmt.Printf("Capture rescheduled: %s\n", captureStart.UTC().Format(time.RFC3339Nano))
	}

	for {
		select {
		case replacementT0 := <-coordinator.t0s:
			applyReplacement(replacementT0)
			continue
		default:
		}

		now := time.Now()
		if !t0Logged && !now.Before(t0) {
			if coordinator.closeReplacementWindow(t0) {
				t0Logged = true
				fmt.Printf("T0 reached: %s\n", now.UTC().Format(time.RFC3339Nano))
			}
		}
		if err := displayStopwatch(renderer, font, outW, outH, now.Sub(t0)); err != nil {
			return t0, err
		}

		for event := sdl.PollEvent(); event != nil; event = sdl.PollEvent() {
			if _, ok := event.(*sdl.QuitEvent); ok {
				return t0, errQuitRequested
			}
		}

		select {
		case replacementT0 := <-coordinator.t0s:
			applyReplacement(replacementT0)

		case <-captureTimer.C:
			if !recordingStarted {
				recordingStarted = true
				go func() {
					recordingDone <- record(mode.recordingDuration)
				}()
			}

		case err := <-recordingDone:
			return t0, err
		case <-ticker.C:
		}
	}
}

func frameStepForClicks(clicks int) int {
	switch clicks {
	case 1:
		return 1
	case 2:
		return 10
	case 3:
		return 50
	default:
		return 100
	}
}

func collectNavigationClicks(
	direction buttonAction,
	buttonActions <-chan buttonAction,
) (buttonAction, int) {
	clicks := 1
	timer := time.NewTimer(400 * time.Millisecond)
	defer timer.Stop()

	for {
		select {
		case action := <-buttonActions:
			if action == buttonConfirm {
				return action, 1
			}
			if action != direction {
				direction = action
				clicks = 1
			} else {
				clicks++
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(400 * time.Millisecond)

		case <-timer.C:
			return direction, clicks
		}
	}
}

func reviewFrames(
	frames []FrameMetadata,
	renderer *sdl.Renderer,
	font *ttf.Font,
	outW int32,
	outH int32,
	t0 time.Time,
	commands <-chan string,
	buttonActions <-chan buttonAction,
) bool {
	index := len(frames) / 2

	for {
		if err := extractFrame(index); err != nil {
			fmt.Printf("failed to extract frame: %v\n", err)
			return false
		}

		frameTime := time.Unix(
			0,
			frames[index].FrameWallClock,
		)

		elapsed := frameTime.Sub(t0)

		if err := displayFrame(
			renderer,
			font,
			outW,
			outH,
			elapsed,
		); err != nil {
			fmt.Printf("failed to display frame: %v\n", err)
			return false
		}

		fmt.Printf(
			"\nframe %d/%d\nFrameWallClock: %d\nTime: %s\nElapsed: %.3f s\n",
			index,
			len(frames)-1,
			frames[index].FrameWallClock,
			frameTime.Format(time.RFC3339Nano),
			elapsed.Seconds(),
		)

		fmt.Print("[n]ext [p]revious [number] [q]uit; GPIO17 new sprint > ")

		var input string
		frameDelta := 0
		select {
		case command, ok := <-commands:
			if !ok {
				commands = nil
				continue
			}
			input = command

		case action := <-buttonActions:
			switch action {
			case buttonConfirm:
				return true
			case buttonPrevious:
				direction, clicks := collectNavigationClicks(action, buttonActions)
				if direction == buttonConfirm {
					return true
				}
				step := frameStepForClicks(clicks)
				fmt.Printf("%d clicks: %d frames\n", clicks, step)
				if direction == buttonPrevious {
					frameDelta = -step
				} else {
					frameDelta = step
				}
			case buttonNext:
				direction, clicks := collectNavigationClicks(action, buttonActions)
				if direction == buttonConfirm {
					return true
				}
				step := frameStepForClicks(clicks)
				fmt.Printf("%d clicks: %d frames\n", clicks, step)
				if direction == buttonPrevious {
					frameDelta = -step
				} else {
					frameDelta = step
				}
			default:
				continue
			}
		}

		if frameDelta != 0 {
			index = max(0, min(len(frames)-1, index+frameDelta))
			continue
		}

		switch input {
		case "n":
			if index < len(frames)-1 {
				index++
			}

		case "p":
			if index > 0 {
				index--
			}

		case "q":
			return false

		default:
			n, err := strconv.Atoi(input)
			if err == nil &&
				n >= 0 &&
				n < len(frames) {
				index = n
			}
		}
	}
}

func main() {
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		log.Fatal(err)
	}
	defer sdl.Quit()

	if err := ttf.Init(); err != nil {
		log.Fatal(err)
	}
	defer ttf.Quit()

	window, renderer, err := sdl.CreateWindowAndRenderer(
		1280,
		720,
		sdl.WINDOW_FULLSCREEN_DESKTOP,
	)
	if err != nil {
		log.Fatal(err)
	}
	defer window.Destroy()
	defer renderer.Destroy()

	outW, outH, err := renderer.GetOutputSize()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("SDL output: %dx%d\n", outW, outH)

	font, err := ttf.OpenFont(
		"/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
		64,
	)
	if err != nil {
		log.Fatal(err)
	}
	defer font.Close()

	commands := readTerminalCommands()
	var buttonActions <-chan buttonAction
	buttons, err := startPiButtons()
	if err != nil {
		log.Printf("Physical buttons disabled: %v", err)
	} else {
		defer buttons.Close()
		buttonActions = buttons.actions
		fmt.Println("Physical buttons ready: GPIO17 new sprint, GPIO27 previous, GPIO22 next")
	}

	coordinator, err := startGunCoordinator()
	if err != nil {
		log.Fatal(err)
	}

	selectedModeIndex := 2
	for {
		drainButtonActions(buttonActions)
		t0, err := waitForT0WithPreview(
			renderer,
			font,
			int32(outW),
			int32(outH),
			commands,
			buttonActions,
			coordinator,
			&selectedModeIndex,
		)
		if errors.Is(err, errQuitRequested) {
			return
		}
		if err != nil {
			log.Printf("live preview failed: %v", err)
			return
		}

		fmt.Printf("t0: %s\n", t0.UTC().Format(time.RFC3339Nano))
		mode := sprintModes[selectedModeIndex]
		finalT0, err := recordWithStopwatch(
			renderer,
			font,
			int32(outW),
			int32(outH),
			t0,
			coordinator,
			mode,
		)
		t0 = finalT0
		if err != nil {
			if !errors.Is(err, errQuitRequested) {
				log.Printf("recording failed: %v", err)
			}
			return
		}

		fmt.Println("Recording completed")
		frames, err := loadMetadata("metadata.json")
		if err != nil {
			log.Printf("failed to load metadata: %v", err)
			return
		}

		fmt.Printf("loaded %d frames\n", len(frames))
		drainButtonActions(buttonActions)
		if !reviewFrames(
			frames,
			renderer,
			font,
			int32(outW),
			int32(outH),
			t0,
			commands,
			buttonActions,
		) {
			return
		}
	}
}
