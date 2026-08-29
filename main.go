package main 

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
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

func reviewFrames(frames []FrameMetadata) {
	index := len(frames) / 2
	reader := bufio.NewReader(os.Stdin)

	for {
		if err := extractFrame(index); err != nil {
			fmt.Printf("failed to extract frame: %v\n", err)
			return
		}

		frameTime := time.Unix(0, frames[index].FrameWallClock)

		fmt.Printf(
			"\nframe %d/%d\nFrameWallClock: %d\nTime: %s\nsaved: current-frame.jpg\n",
			index,
			len(frames)-1,
			frames[index].FrameWallClock,
			frameTime.Format(time.RFC3339Nano),
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
		fmt.Printf("Recording failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Recording completed")

	frames, err := loadMetadata("metadata.json")
	if err != nil {
		fmt.Printf("failed to load metadata: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("loaded %d frames\n", len(frames))

	reviewFrames(frames)
}
