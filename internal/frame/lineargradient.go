package frame

import (
	"image"
	"image/color"
	"io"

	"github.com/rs/zerolog/log"
)

// Creates frames which show a gradient sliding to the left
type LinearGradient struct {
	ColorChannel chan *color.RGBA
	imageChannel chan *image.RGBA
	Transition   int
	Rect         image.Rectangle
	img          *image.RGBA
	idx          int
}

func (lgis *LinearGradient) Read(out []byte) (int, error) {
	cnt := 0
	l := len(out)
	end := false
	imageSize := lgis.Rect.Dx() * lgis.Rect.Dy() * 4
	for cnt < l {
		if lgis.img == nil {
			img, ok := <-lgis.imageChannel
			if !ok {
				end = true
			}
			lgis.img = img
		}
		n := 0
		for i, j := lgis.idx, cnt; i < imageSize && j < l; i, j = i+4, j+4 {
			x := i % lgis.img.Stride
			out[j] = lgis.img.Pix[x]
			out[j+1] = lgis.img.Pix[x+1]
			out[j+2] = lgis.img.Pix[x+2]
			out[j+3] = lgis.img.Pix[x+3]
			n += 4
		}
		lgis.idx += n
		cnt += n
		if lgis.idx >= imageSize {
			lgis.img = nil
			lgis.idx = 0
		}
	}

	var err error
	if end {
		err = io.EOF
	}
	return cnt, err
}

func (lgis *LinearGradient) Run() {
	lgis.imageChannel = make(chan *image.RGBA, lgis.Transition*3)
	var left *color.RGBA
	var middle *color.RGBA
	var right *color.RGBA
	step := lgis.Rect.Dx() / lgis.Transition
	done := false
	getCol := func() *color.RGBA {
		i, ok := <-lgis.ColorChannel
		if !ok {
			done = true
		}
		return i
	}
	stops := [3]int{
		0,
		lgis.Rect.Dx(),
		lgis.Rect.Dx() * 2,
	}
	for !done {
		if left == nil {
			left = getCol()
		}
		if middle == nil {
			middle = getCol()
		}
		if right == nil {
			right = getCol()
		}
		img := image.NewRGBA(image.Rect(0, 0, lgis.Rect.Dx(), 1))
		for x := 0; x < lgis.Rect.Dx(); x++ {
			col := mix(left, middle, lerp(stops[0], stops[1], x))
			col = mix(col, right, lerp(stops[1], stops[2], x))
			img.SetRGBA(x, 0, *col)
		}
		lgis.imageChannel <- img
		stops[0] -= step
		stops[1] -= step
		stops[2] -= step
		if stops[1] <= 0 {
			left = middle
			middle = right
			right = nil
			stops[0] = stops[1]
			stops[1] = stops[2]
			stops[2] = stops[1] + lgis.Rect.Dx()
		}
	}
	close(lgis.imageChannel)
}

// Creates frames that transition from one color to another
type LinearGradientTransition struct {
	ColorChannel chan *color.RGBA
	Transition   int
	ImageWidth   int
	ImageHeight  int
	col          *color.RGBA
	idx          int
	imageChannel chan *color.RGBA
}

func (lgt *LinearGradientTransition) Read(out []byte) (int, error) {
	cnt := 0
	l := len(out)
	end := false
	for cnt < l {
		if lgt.col == nil {
			col, ok := <-lgt.imageChannel
			if !ok {
				end = true
			}
			lgt.col = col
		}
		n := 0
		imageSize := lgt.ImageWidth * lgt.ImageHeight * 4
		for i, j := lgt.idx, cnt; i < imageSize && j < l; i, j = i+4, j+4 {
			out[j] = lgt.col.R
			out[j+1] = lgt.col.B
			out[j+2] = lgt.col.B
			out[j+3] = lgt.col.A
			n += 4
		}
		lgt.idx += n
		cnt += n
		if lgt.idx >= imageSize {
			lgt.col = nil
			lgt.idx = 0
		}
	}
	var err error
	if end {
		err = io.EOF
	}
	return cnt, err
}

func (lgt *LinearGradientTransition) Run() {
	lgt.imageChannel = make(chan *color.RGBA, lgt.Transition*3)
	var left *color.RGBA
	var right *color.RGBA
	done := false
	for !done {
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
			color := mix(left, right, ratio)
			lgt.imageChannel <- color
			// img := image.NewRGBA(image.Rect(0, 0, lgt.ImageWidth, lgt.ImageHeight))
			// for x := 0; x < lgt.ImageWidth; x++ {
			// 	for y := 0; y < lgt.ImageHeight; y++ {
			// 		img.Set(x, y, color)
			// 	}
			// }
			// lgt.imageChannel <- img
		}
		left = right
		right = nil
	}
	close(lgt.imageChannel)
}

// Linear interpolation
func lerp(min int, max int, pos int) float32 {
	v := float32(pos-min) / float32(max-min)
	if v > 1.0 {
		v = 1.0
	}
	if v < 0.0 {
		v = 0.0
	}
	return v
}

// mix two colors
func mix(c1 *color.RGBA, c2 *color.RGBA, ratio float32) *color.RGBA {
	r := uint8(float32(c1.R)*(1.0-ratio) + float32(c2.R)*ratio)
	g := uint8(float32(c1.G)*(1.0-ratio) + float32(c2.G)*ratio)
	b := uint8(float32(c1.B)*(1.0-ratio) + float32(c2.B)*ratio)
	a := uint8(float32(c1.A)*(1.0-ratio) + float32(c2.A)*ratio)
	return &color.RGBA{r, g, b, a}
}
