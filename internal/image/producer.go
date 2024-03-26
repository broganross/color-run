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

type SequenceFrame struct {
	Frame int
	Image []byte
}

type ProducerOptions struct {
	ColorMind              *colormind.ColorMind
	RandomColorModel       bool
	ImageChannelBufferSize int
	TransitionFrames       int
	ImageEncoder           ImageEncoder
	ImageRect              stdimage.Rectangle
	ColorQueueSize         int
}

func NewProducer(opts *ProducerOptions) *Producer {
	if opts.ColorMind == nil {
		opts.ColorMind = colormind.New()
	}
	if opts.ImageChannelBufferSize <= 0 {
		opts.ImageChannelBufferSize = 60
	}
	if opts.TransitionFrames <= 0 {
		opts.TransitionFrames = 90
	}
	if opts.ColorQueueSize <= 0 {
		opts.ColorQueueSize = 15
	}
	p := &Producer{
		ColorMind:        opts.ColorMind,
		imgChanSize:      opts.ImageChannelBufferSize,
		ImageChannel:     make(chan *SequenceFrame, opts.ImageChannelBufferSize),
		ErrorChannel:     make(chan error, opts.ImageChannelBufferSize),
		TransitionFrames: opts.TransitionFrames,
		ImageEncoder:     opts.ImageEncoder,
		ImageRect:        opts.ImageRect,
		colorQueueSize:   opts.ColorQueueSize,
		randomColorModel: opts.RandomColorModel,
		model:            "default",
	}
	return p
}

type Producer struct {
	ColorMind        *colormind.ColorMind
	ImageRect        stdimage.Rectangle
	TransitionFrames int
	ImageEncoder     ImageEncoder
	// Output Channels
	ImageChannel chan *SequenceFrame
	ErrorChannel chan error
	// internal
	imgChanSize      int
	model            string
	colorQueueSize   int
	colorQueue       chan *color.RGBA
	stopping         bool
	randomColorModel bool
}

func (p *Producer) getPalettes() {
	// TODO: Add something that finds duplicate colors over time
	var previous *colormind.Palette
	// TODO: The palettes come back just a little bit different so this may not be needed
	// look into whether it's worth it
	start := 0
	slowCount := p.colorQueueSize / 3
	for !p.stopping {
		log.Debug().Int("slow_count", slowCount).Msg("getting palette")
		pal, err := p.ColorMind.GetPalette(p.model, previous)
		if err != nil {
			p.ErrorChannel <- fmt.Errorf("getting palette: %w", err)
			continue
		}
		for i := start; i < len(pal); i++ {
			p.colorQueue <- pal[i]
		}
		if previous == nil {
			previous = &colormind.Palette{}
			start = 2
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
	log.Debug().Msg("getPalettes completed")
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
		img := stdimage.NewRGBA(p.ImageRect)
		for frame := 0; frame < p.TransitionFrames; frame++ {
			ratio := float32(frame) / float32(p.TransitionFrames)
			imgCol := lerp(left, right, ratio)
			for x := 0; x < img.Rect.Dx(); x++ {
				for y := 0; y < img.Rect.Dy(); y++ {
					img.Set(x, y, imgCol)
				}
			}
			out := &SequenceFrame{
				Frame: lastFrame + frame,
			}
			buffer := &bytes.Buffer{}
			if err := p.ImageEncoder(buffer, img); err != nil {
				p.ErrorChannel <- fmt.Errorf("%w (%06d): %w", ErrEncodeImage, out.Frame, err)
				continue
			}
			out.Image = buffer.Bytes()
			p.ImageChannel <- out
		}
		lastFrame += p.TransitionFrames
		left = right
		right = nil
		log.Debug().Int("frames_per_second", lastFrame/int(time.Since(start).Seconds())).Msg("speed")
	}
	log.Debug().Msg("makeImages closed")
}

func (p *Producer) Start() error {
	p.colorQueue = make(chan *color.RGBA, p.colorQueueSize)
	p.stopping = false

	if p.randomColorModel {
		models, err := p.ColorMind.ListModels()
		if err != nil {
			return fmt.Errorf("getting models: %w", err)
		}
		p.model = models[rand.Intn(len(models))]
		log.Debug().Str("model", p.model).Send()
	}
	go p.getPalettes()
	p.makeImages()

	return nil
}

func (p *Producer) Stop() {
	p.stopping = true
	log.Debug().Msg("stopping")
}
