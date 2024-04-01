package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
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
	"github.com/broganross/color-run/internal/config"
	"github.com/broganross/color-run/internal/frame"
	"github.com/kelseyhightower/envconfig"
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
	ImageChannel chan *image.RGBA
	img          *image.RGBA
	idx          int
}

func (fi *ffmpegInput) Read(p []byte) (int, error) {
	cnt := 0
	l := len(p)
	end := false
	for {
		if fi.img == nil {
			img, ok := <-fi.ImageChannel
			if !ok {
				end = true
			}
			fi.img = img
		}
		n := 0
		imageSize := fi.img.Rect.Dx() * fi.img.Rect.Dy() * 4
		for i, j := fi.idx, cnt; i < imageSize && j < l; i, j = i+4, j+4 {
			p[j] = fi.img.Pix[i]
			p[j+1] = fi.img.Pix[i+1]
			p[j+2] = fi.img.Pix[i+2]
			p[j+3] = fi.img.Pix[i+3]
			n += 4
		}
		fi.idx += n
		cnt += n
		if fi.idx >= imageSize {
			fi.img = nil
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

type errorOut struct{}

func (eo *errorOut) Write(p []byte) (int, error) {
	os.Stderr.Write([]byte("Error: "))
	n, err := os.Stderr.Write(p)
	os.Stderr.Write([]byte("\n"))
	return n, err
}

func main() {
	conf := config.Config{}
	if err := envconfig.Process("colorrun", &conf); err != nil {
		log.Error().Err(err).Msg("parsing environment variables")
		os.Exit(1)
	}
	flag.IntVar(&conf.ImageWidth, "w", conf.ImageWidth, "image width")
	flag.IntVar(&conf.ImageHeight, "h", conf.ImageHeight, "image height")
	flag.IntVar(&conf.FrameCount, "f", conf.FrameCount, "number of frames to transition from one color to another")
	flag.BoolVar(&conf.RandomModel, "r", conf.RandomModel, "use a random color mind model")
	flag.StringVar(&conf.StreamKey, "k", conf.StreamKey, "twitch stream key")
	flag.StringVar(&conf.DumpDir, "d", conf.DumpDir, "dump frames to this directory as well as streaming")
	flag.Parse()
	if conf.StreamKey == "" {
		log.Fatal().Msg("stream key not set")
	}
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	ctx := context.Background()
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	colorChanSize := 15
	// color palette channel
	errorChannel := make(chan error, 5)
	// frame color channel
	frameChannel := make(chan *image.RGBA, conf.FrameCount*3)
	httpClient := &http.Client{}

	// creates the color mind client and retrieves a random color palette
	cm := colormind.New()
	cm.Client = httpClient
	colorModel := "default"
	if conf.RandomModel {
		models, err := cm.ListModelsWithContext(ctx)
		if err != nil {
			log.Error().Err(err).Msg("getting color mind models")
			os.Exit(1)
		}
		colorModel = models[rand.Intn(len(models))]
	}
	colorChannel, colErrChan := colormind.PaletteQueue(ctx, colorModel, cm, colorChanSize)

	ingestURL, err := getIngestURL(ctx, httpClient, conf.StreamKey)
	if err != nil {
		log.Error().Err(err).Msg("getting ingest URL")
		os.Exit(1)
	}

	input := &ffmpegInput{
		ImageChannel: frameChannel,
	}
	outPath := ingestURL
	if conf.DumpDir != "" {
		outPath = filepath.Join(conf.DumpDir, "out.flv")
	}

	proc := ffmpeg.
		Input("pipe:0", ffmpeg.KwArgs{
			"f":          "rawvideo",
			"pix_fmt":    "rgba",
			"video_size": fmt.Sprintf("%dx%d", conf.ImageWidth, conf.ImageHeight),
		}).
		WithInput(input).
		Output(outPath, ffmpeg.KwArgs{
			"framerate": 30,
			"c:v":       "libx264",
			"preset":    "veryfast",
			"f":         "flv",
		}).
		WithOutput(os.Stdout).
		WithErrorOutput(&errorOut{}).
		Compile()

	if err != nil {
		log.Error().Err(err).Msg("getting stderr pipe")
		os.Exit(10)
	}
	go func() {
		log.Info().Msg("waiting for ffmpeg")
		if err := proc.Run(); err != nil {
			errorChannel <- fmt.Errorf("%w: %w", errFfmpegExit, err)
		}
		// ffmpeg has inconsitent exit codes, TODO: figure out a way to handle this so that we stop when ffmpeg fails
		log.Info().Int("exit-code", proc.ProcessState.ExitCode()).Msg("ffmpeg exited")
		errorChannel <- errFfmpegExit
	}()
	frameMaker := frame.LinearGradientTransition{
		ImageWidth:   conf.ImageWidth,
		ImageHeight:  conf.ImageHeight,
		Transition:   conf.FrameCount,
		ColorChannel: colorChannel,
		ImageChannel: frameChannel,
	}
	go frameMaker.Run()

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
		case err := <-colErrChan:
			log.Error().Err(err).Send()
		}
		if done {
			break
		}
	}

	os.Exit(0)
}
