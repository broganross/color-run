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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/broganross/color-run/internal/colormind"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	ffmpeg "github.com/u2takey/ffmpeg-go"
)

var Version = "development"
var ErrInputClosed = errors.New("input channel has been closed")
var errFfmpegExit = errors.New("ffmpeg errorred")

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
	ColorChannel chan *color.RGBA
	ImageSize    int
	col          *color.RGBA
	idx          int
}

func (fi *ffmpegInput) Read(p []byte) (int, error) {
	cnt := 0
	l := len(p)
	end := false
	for {
		if fi.col == nil {
			col, ok := <-fi.ColorChannel
			if !ok {
				end = true
			}
			fi.col = col
		}
		n := 0
		for i, j := fi.idx, cnt; i < fi.ImageSize*4 && j < l; i, j = i+1, j+4 {
			p[j] = fi.col.R
			p[j+1] = fi.col.G
			p[j+2] = fi.col.B
			p[j+3] = fi.col.A
			n += 4
		}
		fi.idx += n
		cnt += n
		if fi.idx >= fi.ImageSize*4 {
			fi.col = nil
			fi.idx = 0
		}
		if cnt >= l {
			break
		}
	}
	var err error
	if end {
		err = io.EOF
	}
	return cnt, err
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

func main() {
	width := flag.Int("w", 1280, "image width")
	height := flag.Int("h", 720, "image height")
	transitionFrames := flag.Int("f", 90, "number of frames to transition from one color to another")
	randomModel := flag.Bool("r", false, "use a random color mind model")
	streamKey := flag.String("k", "", "twitch stream key")
	dumpDir := flag.String("d", "", "dump frames to this directory as well as streaming")
	flag.Parse()
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	ctx := context.Background()
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	colorChanSize := 15
	// color palette channel
	colorChannel := make(chan *color.RGBA, colorChanSize)
	errorChannel := make(chan error, 5)
	// frame color channel
	frameChannel := make(chan *color.RGBA, *transitionFrames*3)
	httpClient := &http.Client{}

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
	// get palettes as long as we need to
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
			log.Debug().Any("palette", pal).Msg("got palette")
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

	imageSize := *width * *height
	input := &ffmpegInput{
		ColorChannel: frameChannel,
		ImageSize:    imageSize,
	}
	outPath := ingestURL
	format := "flv"
	if *dumpDir != "" {
		format = "mov"
		outPath = filepath.Join(*dumpDir, "out.mov")
	}

	proc := ffmpeg.Input("pipe:0", ffmpeg.KwArgs{
		"f":          "rawvideo",
		"pix_fmt":    "rgba",
		"video_size": fmt.Sprintf("%dx%d", *width, *height),
	}).
		WithInput(input).
		Output(outPath, ffmpeg.KwArgs{
			"framerate": 30,
			"c:v":       "libx264",
			"preset":    "veryfast",
			"f":         format,
		}).
		ErrorToStdOut().
		Compile()
	if err := proc.Start(); err != nil {
		log.Error().Err(err).Msg("starting ffmpeg")
		os.Exit(1)
	}

	go func() {
		// gotta figure out how to make this exit if ffmpeg fails
		if err := proc.Wait(); err != nil {
			errorChannel <- fmt.Errorf("%w: %w", errFfmpegExit, err)
		}
	}()
	go func() {
		var left *color.RGBA
		var right *color.RGBA
		done := false
		for {
			if left == nil {
				l, ok := <-colorChannel
				if !ok {
					done = true
				}
				left = l
			}
			if right == nil {
				r, ok := <-colorChannel
				if !ok {
					done = true
				}
				right = r
			}
			log.Debug().Msg("got left and right")
			for frame := 0; frame < *transitionFrames; frame++ {
				ratio := float32(frame) / float32(*transitionFrames)
				color := lerp(left, right, ratio)
				frameChannel <- color
			}
			left = right
			right = nil
			if done {
				break
			}
		}
		close(frameChannel)
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
			if errors.Is(err, errFfmpegExit) {
				stop()
				done = true
			}
		}
		if done {
			break
		}
	}

	os.Exit(0)
}
