package image

import (
	"bytes"
	"errors"
	"fmt"
	stdimage "image"
	"image/color"
	"io"
	"math/rand"
	"time"

	"github.com/broganross/color-run/internal/colormind"
	"github.com/rs/zerolog/log"
)

var (
	ErrImageChannelNotSet = errors.New("image channel not set")
	ErrErrorChannelNotSet = errors.New("error channel not set")
	ErrEncodeImage        = errors.New("encoding image")
)

type ImageEncoder func(w io.Writer, m stdimage.Image) error

type solidImage struct {
	stdimage.Uniform
	Rect stdimage.Rectangle
}

func (si *solidImage) Bounds() stdimage.Rectangle {
	return si.Rect
}

type SequenceFrame struct {
	Frame int
	Image []byte
}

type Producer struct {
	ColorMind        *colormind.ColorMind
	ReferenceImage   string
	Rect             stdimage.Rectangle
	TransitionFrames int
	ImageEncoder     ImageEncoder
	// Output Channels
	ImageChannel chan *SequenceFrame
	ErrorChannel chan error
	// internal
	model      string
	colorQueue chan *color.RGBA
	stopping   bool
}

func (p *Producer) fillColors() {
	var previous *colormind.Palette
	slowCount := 10
	for !p.stopping {
		log.Debug().Int("slow_count", slowCount).Msg("getting palette")
		pal, err := p.ColorMind.GetPalette(p.model, previous)
		if err != nil {
			p.ErrorChannel <- fmt.Errorf("getting palette: %w", err)
		} else {
			for i := 0; i < len(pal); i++ {
				p.colorQueue <- pal[i]
			}
		}
		if previous == nil {
			previous = &colormind.Palette{}
		}
		previous[0] = pal[3]
		previous[1] = pal[4]
		// wait a little between each call to API until the buffer is full
		if slowCount > 0 {
			time.Sleep(2 * time.Second)
			slowCount--
		}
	}
	close(p.colorQueue)
}

func (p *Producer) makeImages() {
	var left *color.RGBA
	var right *color.RGBA
	lastFrame := 0
	start := time.Now()
	for col := range p.colorQueue {
		if left == nil {
			left = col
			continue
		}
		if right == nil {
			right = col
			continue
		}
		log.Debug().Msg("color pair found")
		for frame := 0; frame < p.TransitionFrames; frame++ {
			// log.Debug().Int("total_frame", lastFrame+frame).Int("transition_frame", frame).Msg("starting frame")
			img := solidImage{
				Uniform: *stdimage.NewUniform(&color.RGBA{}),
				Rect:    p.Rect,
			}
			ratio := float32(frame) / float32(p.TransitionFrames)
			img.Uniform.C = lerp(left, right, ratio)
			out := &SequenceFrame{
				Frame: lastFrame + frame,
			}
			buffer := &bytes.Buffer{}
			if err := p.ImageEncoder(buffer, &img); err != nil {
				p.ErrorChannel <- fmt.Errorf("%w (%06d): %w", ErrEncodeImage, out.Frame, err)
				continue
			}
			out.Image = buffer.Bytes()
			p.ImageChannel <- out
		}
		lastFrame += p.TransitionFrames
		left = right
		right = nil
		log.Debug().Int("frames_per_second", lastFrame/int(time.Now().Sub(start).Seconds())).Msg("speed")
	}
}

func (p *Producer) Start() error {
	p.colorQueue = make(chan *color.RGBA, 16)
	if p.ImageChannel == nil {
		return ErrImageChannelNotSet
	}
	if p.ErrorChannel == nil {
		return ErrErrorChannelNotSet
	}

	models, err := p.ColorMind.ListModels()
	if err != nil {
		return fmt.Errorf("getting models: %w", err)
	}
	p.model = models[rand.Intn(len(models))]
	log.Debug().Str("model", p.model).Send()

	go p.fillColors()
	p.makeImages()

	return nil
}

func (p *Producer) Stop() {
	p.stopping = true
}
