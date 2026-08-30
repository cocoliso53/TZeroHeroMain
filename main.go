package main

import (
	"bufio"
	"encoding/json"
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

func calibrate(
	renderer *sdl.Renderer,
	outW int32,
	outH int32,
) error {
	fmt.Println("Calibration mode")
	fmt.Println("Align the red line with the finish line.")
	fmt.Print("[s]tart [q]uit > ")

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
		return err
	}

	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return err
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

	// NEW: terminal input, same style as review mode
	input := make(chan string)

	go func() {
		reader := bufio.NewReader(os.Stdin)

		for {
			text, err := reader.ReadString('\n')
			if err != nil {
				close(input)
				return
			}

			input <- strings.TrimSpace(text)
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
		// NEW: terminal commands
		select {
		case command, ok := <-input:
			if !ok {
				stopCamera()
				return fmt.Errorf("stdin closed")
			}

			switch command {
			case "s":
				fmt.Println("Starting sprint sequence")

				stopCamera()

				if texture != nil {
					texture.Destroy()
					texture = nil
				}

				return nil

			case "q":
				stopCamera()
				os.Exit(0)

			default:
				fmt.Print("[s]tart [q]uit > ")
			}

		default:
		}

		// Keep SDL responsive too.
		for event := sdl.PollEvent(); event != nil; event = sdl.PollEvent() {
			switch event.(type) {
			case *sdl.QuitEvent:
				stopCamera()
				os.Exit(0)
			}
		}

		select {
		case frame, ok := <-frames:
			if !ok {
				return fmt.Errorf("camera preview stopped unexpectedly")
			}

			newTexture, err := textureFromJPEG(renderer, frame)
			if err != nil {
				return err
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
				return err
			}
		}

		if err := drawFinishLine(renderer, outW, outH); err != nil {
			return err
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

func reviewFrames(
	frames []FrameMetadata,
	renderer *sdl.Renderer,
	font *ttf.Font,
	outW int32,
	outH int32,
	t0 time.Time,
) {
	index := len(frames) / 2
	reader := bufio.NewReader(os.Stdin)

	for {
		if err := extractFrame(index); err != nil {
			fmt.Printf("failed to extract frame: %v\n", err)
			return
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

	if err := calibrate(
		renderer,
		int32(outW),
		int32(outH),
	); err != nil {
		log.Fatal(err)
	}

	t0, gunConnection, err := waitForGunT0()
	if err != nil {
		log.Fatal(err)
	}
	defer gunConnection.Close()

	delta := 5 * time.Second

	captureStart := t0.Add(delta)

	fmt.Printf("t0: %s\n", t0.UTC().Format(time.RFC3339Nano))
	fmt.Printf(
		"Capture starts: %s\n",
		captureStart.UTC().Format(time.RFC3339Nano),
	)

	go func() {
		time.Sleep(time.Until(t0))
		fmt.Printf("T0 reached: %s\n", time.Now().UTC().Format(time.RFC3339Nano))
	}()

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

	reviewFrames(
		frames,
		renderer,
		font,
		int32(outW),
		int32(outH),
		t0,
	)
}
