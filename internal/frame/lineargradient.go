package frame

import (
	"image"
	"image/color"

	"github.com/rs/zerolog/log"
)

type LinearGradientTransition struct {
	ColorChannel chan *color.RGBA
	ImageChannel chan *image.RGBA
	Transition   int
	ImageWidth   int
	ImageHeight  int
}

func (lgt *LinearGradientTransition) Run() {
	var left *color.RGBA
	var right *color.RGBA
	done := false
	for {
		if left == nil {
			l, ok := <-lgt.ColorChannel
			if !ok {
				done = true
			}
			left = l
		}
		if right == nil {
			r, ok := <-lgt.ColorChannel
			if !ok {
				done = true
			}
			right = r
		}
		log.Debug().Msg("got left and right")
		for frame := 0; frame < lgt.Transition; frame++ {
			ratio := float32(frame) / float32(lgt.Transition)
			color := lerp(left, right, ratio)
			img := image.NewRGBA(image.Rect(0, 0, lgt.ImageWidth, lgt.ImageHeight))
			for x := 0; x < lgt.ImageWidth; x++ {
				for y := 0; y < lgt.ImageHeight; y++ {
					img.Set(x, y, color)
				}
			}
			lgt.ImageChannel <- img
		}
		left = right
		right = nil
		if done {
			break
		}
	}
	close(lgt.ImageChannel)
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
