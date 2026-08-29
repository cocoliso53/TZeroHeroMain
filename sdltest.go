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

	fmt.Println("Window and renderer created") // NEW

	defer window.Destroy()
	defer renderer.Destroy()

	fmt.Println("Loading image") // NEW

	texture, err := img.LoadTexture(renderer, "current-frame.jpg")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Image loaded") // NEW
	defer texture.Destroy()

	renderer.Clear()
	renderer.Copy(texture, nil, nil)
	renderer.Present()

	fmt.Println("Image presented") // NEW

	for {
		event := sdl.WaitEvent()

		switch event.(type) {
		case *sdl.QuitEvent, *sdl.KeyboardEvent:
			return
		}
	}
}
