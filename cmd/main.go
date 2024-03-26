package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image/color"
	"io"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/broganross/color-run/internal/colormind"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	ffmpeg "github.com/u2takey/ffmpeg-go"
)

var ErrInputClosed = errors.New("input channel has been closed")

type ingestsResponse struct {
	Ingests []struct {
		ID           int     `json:"_id"`
		Availability float64 `json:"availability"`
		Default      bool    `json:"default"`
		Name         string  `json:"name"`
		URLTemplate  string  `json:"url_template"`
		Priority     int     `json:"priority"`
	} `json:"ingests"`
}

type ffmpegInput struct {
	Channel chan []byte
}

func (fi *ffmpegInput) Read(p []byte) (int, error) {
	// TODO: add time out, and signal cancellation handling
	cnt := 0
	end := false
	for cnt < len(p) {
		b, ok := <-fi.Channel
		if !ok {
			end = true
		}
		copy(p[cnt:], b)
		cnt += len(b)
	}
	var err error
	if end {
		err = io.EOF
	}
	log.Debug().Msg("read called")
	return cnt, err
}

func main() {
	width := flag.Int("w", 1280, "image width")
	height := flag.Int("h", 720, "image height")
	transitionFrames := flag.Int("f", 90, "number of frames to transition from one color to another")
	randomModel := flag.Bool("r", false, "use a random color mind model")
	streamKey := flag.String("k", "", "twitch stream key")
	_ = streamKey
	flag.Parse()
	zerolog.SetGlobalLevel(zerolog.DebugLevel)

	ctx := context.Background()
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	colorChanSize := 15
	colorChannel := make(chan *color.RGBA, 15)
	errorChannel := make(chan error, 5)
	httpClient := &http.Client{}
	imageSize := *width * *height
	imageChanSize := imageSize * 4 * *transitionFrames
	// this is for sending each pixel
	imageChannel := make(chan []byte, imageChanSize)

	// creates the color mind client and retrieves a random color palette
	cm := colormind.New()
	cm.Client = httpClient
	colorModel := "default"
	if *randomModel {
		models, err := cm.ListModelsWithContext(ctx)
		if err != nil {
			log.Error().Err(err).Msg("getting color mind models")
			os.Exit(1)
		}
		colorModel = models[rand.Intn(len(models))]
	}
	go func() {
		start := 0
		slowCount := colorChanSize / 3
		var previous *colormind.Palette
		stop := false
		for {
			pal, err := cm.GetPaletteWithContext(ctx, colorModel, previous)
			if err != nil {
				errorChannel <- fmt.Errorf("getting palette: %w", err)
				continue
			}
			for i := start; i < len(pal); i++ {
				select {
				case colorChannel <- pal[i]:
				case <-ctx.Done():
					stop = true
				}
			}
			if previous == nil {
				previous = &colormind.Palette{}
				start = 2
			}
			previous[0] = pal[3]
			previous[1] = pal[4]
			if slowCount > 0 {
				time.Sleep(2 * time.Second)
				slowCount--
			}
			if stop {
				break
			}
		}
		close(colorChannel)
	}()

	ingestURL, err := getIngestURL(ctx, httpClient, *streamKey)
	if err != nil {
		log.Error().Err(err).Msg("getting ingest URL")
		os.Exit(1)
	}

	input := &ffmpegInput{Channel: imageChannel}
	proc := ffmpeg.Input("pipe:0", ffmpeg.KwArgs{
		"f":          "rawvideo",
		"pix_fmt":    "rgba",
		"video_size": fmt.Sprintf("%dx%d", *width, *height),
	}).
		WithInput(input).
		Output(ingestURL, ffmpeg.KwArgs{
			"framerate": 30,
			"c:v":       "libx264",
			"preset":    "veryfast",
			"f":         "flv",
		}).
		SetFfmpegPath("C:\\Program Files\\ffmpeg\\bin\\ffmpeg.exe").
		ErrorToStdOut()
	go func() {
		log.Debug().Msg("starting ffmpeg process")
		err = proc.Run()
		if err != nil {
			errorChannel <- fmt.Errorf("ffmpeg exit error: %w", err)
		}
	}()

	for {
		done := false
		select {
		case <-ctx.Done():
			stop()
			log.Info().Msg("shutting down")
			done = true
		case err := <-errorChannel:
			log.Error().Err(err).Send()
		case left, ok := <-colorChannel:
			if !ok {
				break
			}
			right, ok := <-colorChannel
			if !ok {
				break
			}
			log.Debug().Msg("got left and right")
			for frame := 0; frame < *transitionFrames; frame++ {
				ratio := float32(frame) / float32(*transitionFrames)
				color := lerp(left, right, ratio)
				for x := 0; x < imageSize; x++ {
					imageChannel <- []byte{color.R, color.G, color.B, color.A}
				}
			}
		}
		// TODO: This stops before all images are made
		if done {
			break
		}
	}
	os.Exit(0)
}

func getIngestURL(ctx context.Context, client *http.Client, streamKey string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://ingest.twitch.tv/ingests", nil)
	if err != nil {
		return "", fmt.Errorf("making http request: %w", err)
	}
	ingestResp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("getting ingests")
	} else if ingestResp.StatusCode < http.StatusOK || ingestResp.StatusCode > http.StatusIMUsed {
		defer ingestResp.Body.Close()
		b, err := io.ReadAll(ingestResp.Body)
		if err != nil {
			return "", fmt.Errorf("reading ingest response body: %w", err)
		}
		err = fmt.Errorf("getting ingest (%s): %s", http.StatusText(ingestResp.StatusCode), string(b))
		return "", err
	}
	defer ingestResp.Body.Close()
	r := ingestsResponse{}
	if err := json.NewDecoder(ingestResp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("decoding ingest response: %w", err)
	}
	var ingestURL string
	for _, i := range r.Ingests {
		if i.Default {
			ingestURL = i.URLTemplate
		}
	}
	if ingestURL == "" {
		return "", fmt.Errorf("no default ingest server found")
	}
	ingestURL = strings.Replace(ingestURL, "{stream_key}", streamKey, -1)
	return ingestURL, nil
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
