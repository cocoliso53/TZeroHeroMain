package main

import (
	"fmt"
	"log"

	"github.com/veandco/go-sdl2/img"
	"github.com/veandco/go-sdl2/sdl"
)

func main() {
	fmt.Println("Before SDL init")

	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		log.Fatal(err)
	}

	fmt.Println("After SDL init")

	driver, err := sdl.GetCurrentVideoDriver()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("SDL video driver:", driver)

	defer sdl.Quit()

	window, renderer, err := sdl.CreateWindowAndRenderer(
		1280,
		720,
		sdl.WINDOW_FULLSCREEN_DESKTOP,
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Window and renderer created")

	defer window.Destroy()
	defer renderer.Destroy()

	fmt.Println("Loading image")

	texture, err := img.LoadTexture(renderer, "current-frame.jpg")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Image loaded")

	defer texture.Destroy()

	_, _, texW, texH, err := texture.Query()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Texture size: %dx%d\n", texW, texH)

	outW, outH, err := renderer.GetOutputSize()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Renderer output size: %dx%d\n", outW, outH)

	dst := sdl.Rect{
		X: 0,
		Y: 0,
		W: int32(outW),
		H: int32(outH),
	}

	// NEW: render continuously instead of presenting only once
	for {
		for event := sdl.PollEvent(); event != nil; event = sdl.PollEvent() {
			switch event.(type) {
			case *sdl.QuitEvent, *sdl.KeyboardEvent:
				return
			}
		}

		renderer.SetDrawColor(0, 0, 0, 255)
		renderer.Clear()

		if err := renderer.Copy(texture, nil, &dst); err != nil {
			log.Fatal(err)
		}

		renderer.Present()

		sdl.Delay(16)
	}
}
