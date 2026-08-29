package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/img"
	"github.com/veandco/go-sdl2/sdl"

	// NEW: text rendering
	"github.com/veandco/go-sdl2/ttf"
)

type FrameMetadata struct {
	FrameWallClock int64 `json:"FrameWallClock"`
}

func record() error {
	cmd := exec.Command(
		"rpicam-vid",
		"-t", "5000",
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

	// NEW: draw finish line in the middle of the screen
	lineX := outW / 2

	renderer.SetDrawColor(255, 0, 0, 255)

	// draw a slightly thicker vertical line
	if err := renderer.DrawLine(lineX-1, 0, lineX-1, outH); err != nil {
		return err
	}
	if err := renderer.DrawLine(lineX, 0, lineX, outH); err != nil {
		return err
	}
	if err := renderer.DrawLine(lineX+1, 0, lineX+1, outH); err != nil {
		return err
	}

	// elapsed time label
	label := fmt.Sprintf("%.3f s", elapsed.Seconds())

	surface, err := font.RenderUTF8Blended(
		label,
		sdl.Color{R: 255, G: 255, B: 255, A: 255},
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

func reviewFrames(
	frames []FrameMetadata,
	renderer *sdl.Renderer,
	font *ttf.Font, // NEW
	outW int32,
	outH int32,
	t0 time.Time, // NEW
) {
	index := len(frames) / 2
	reader := bufio.NewReader(os.Stdin)

	for {
		if err := extractFrame(index); err != nil {
			fmt.Printf("failed to extract frame: %v\n", err)
			return
		}

		frameTime := time.Unix(0, frames[index].FrameWallClock)

		// NEW: actual elapsed time from T0 to this physical frame
		elapsed := frameTime.Sub(t0)

		if err := displayFrame(
			renderer,
			font,
			outW,
			outH,
			elapsed,
		); err != nil {
			fmt.Printf("failed to display frame: %v\n", err)
			return
		}

		fmt.Printf(
			"\nframe %d/%d\nFrameWallClock: %d\nTime: %s\nElapsed: %.3f s\n",
			index,
			len(frames)-1,
			frames[index].FrameWallClock,
			frameTime.Format(time.RFC3339Nano),
			elapsed.Seconds(),
		)

		fmt.Print("[n]ext [p]revious [number] [q]uit > ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

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
			return

		default:
			n, err := strconv.Atoi(input)
			if err == nil && n >= 0 && n < len(frames) {
				index = n
			}
		}
	}
}

func main() {
	t0 := time.Now().Add(10 * time.Second)
	delta := 5 * time.Second

	captureStart := t0.Add(delta)

	fmt.Printf("t0: %s\n", t0.Format(time.RFC3339Nano))
	fmt.Printf("Capture starts: %s\n", captureStart.Format(time.RFC3339Nano))

	time.Sleep(time.Until(captureStart))

	if err := record(); err != nil {
		fmt.Printf("recording failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Recording completed")

	frames, err := loadMetadata("metadata.json")
	if err != nil {
		fmt.Printf("failed to load metadata: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("loaded %d frames\n", len(frames))

	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		log.Fatal(err)
	}
	defer sdl.Quit()

	// NEW: initialize SDL_ttf
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

	// NEW: load one font and reuse it for every frame
	font, err := ttf.OpenFont(
		"/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
		64,
	)
	if err != nil {
		log.Fatal(err)
	}
	defer font.Close()

	reviewFrames(
		frames,
		renderer,
		font, // NEW
		int32(outW),
		int32(outH),
		t0, // NEW
	)
}
