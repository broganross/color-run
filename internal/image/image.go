package image

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"

	"github.com/rs/zerolog/log"
)

// Make a reference image
func makeGradient(colors []*color.RGBA, width int, height int, out io.WriteCloser) error {
	log.Debug().Msg("making reference image")
	segmentWidth := (width * len(colors)) / (len(colors) - 1)
	img := image.NewRGBA(image.Rect(0, 0, width*len(colors), height))
	for x := 0; x < width*len(colors); x++ {
		segment := x / segmentWidth
		startCol := colors[segment]
		endCol := colors[segment+1]
		// find pixel position in gradient
		ratio := float32(x%segmentWidth) / float32(segmentWidth)
		col := lerp(startCol, endCol, ratio)
		for y := 0; y < height; y++ {
			img.Set(x, y, col)
		}
	}
	if err := png.Encode(out, img); err != nil {
		return fmt.Errorf("encoding image: %w", err)
	}
	return nil
}

// Linearly interpolate between two colors.
func lerp(c1 *color.RGBA, c2 *color.RGBA, ratio float32) *color.RGBA {
	return &color.RGBA{
		uint8(float32(c1.R)*(1.0-ratio) + float32(c2.R)*ratio),
		uint8(float32(c1.G)*(1.0-ratio) + float32(c2.G)*ratio),
		uint8(float32(c1.B)*(1.0-ratio) + float32(c2.B)*ratio),
		uint8(float32(c1.A)*(1.0-ratio) + float32(c2.A)*ratio),
	}
}
