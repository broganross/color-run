package main

import (
	"context"
	"flag"
	"fmt"
	stdimage "image"
	"image/png"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/broganross/color-run/internal/colormind"
	"github.com/broganross/color-run/internal/image"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	refImage := flag.String("r", "", "reference image showing linear gradient")
	outPath := flag.String("o", "./", "output path of the image")
	width := flag.Int("w", 1280, "image width")
	height := flag.Int("h", 720, "image height")
	transitionFrames := flag.Uint("f", 48, "number of frames to transition from one color to another")
	flag.Parse()
	zerolog.SetGlobalLevel(zerolog.DebugLevel)

	ctx := context.Background()
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// creates the color mind client and retrieves a random color palette
	cm := colormind.New()
	cm.Ctx = ctx
	cm.Client.Timeout = 5 * time.Second

	errChan := make(chan error)
	imgChan := make(chan *image.SequenceFrame, 50)
	imageMaker := image.Producer{
		ColorMind:        cm,
		ReferenceImage:   *refImage,
		Rect:             stdimage.Rect(0, 0, *width, *height),
		TransitionFrames: int(*transitionFrames),
		ImageEncoder:     png.Encode,
		ErrorChannel:     errChan,
		ImageChannel:     imgChan,
	}
	go imageMaker.Start()

	for {
		done := false
		select {
		case <-ctx.Done():
			stop()
			log.Info().Msg("shutting down")
			done = true
			imageMaker.Stop()
		case err := <-errChan:
			log.Error().Err(err).Msg("image generator")
		case frame := <-imgChan:
			// log.Debug().Int("frame", frame.Frame).Msg("got frame")
			if err := writeFrame(frame, *outPath); err != nil {
				log.Error().Err(err).Msg("writing to disk")
			}
		}
		if done {
			break
		}
	}
	os.Exit(0)
}

func writeFrame(img *image.SequenceFrame, outDir string) error {
	fileName := filepath.Join(outDir, fmt.Sprintf("%06d.png", img.Frame))
	f, err := os.Create(fileName)
	if err != nil {
		return fmt.Errorf("opening image file: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(img.Image); err != nil {
		return fmt.Errorf("writing file: %w", err)
	}
	return nil
}
