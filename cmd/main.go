package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	"syscall"

	"github.com/broganross/color-run/internal/colormind"
	"github.com/broganross/color-run/internal/config"
	"github.com/broganross/color-run/internal/frame"
	"github.com/broganross/color-run/internal/twitch"
	"github.com/kelseyhightower/envconfig"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	ffmpeg "github.com/u2takey/ffmpeg-go"
)

var Version = "development"
var ErrInputClosed = errors.New("input channel has been closed")
var errFfmpegExit = errors.New("ffmpeg errorred")

func memDump(filePath string) {
	f, err := os.Create(filePath)
	if err != nil {
		log.Error().Err(err).Msg("creating memory profile output")
		os.Exit(1)
	}
	pprof.WriteHeapProfile(f)
	defer f.Close()
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
	flag.StringVar(&conf.LogLevel, "l", conf.LogLevel, "logging verbosity")
	cpuProfile := flag.String("cpu-profile", "", "cpu profiling output path")
	memProfile := flag.String("mem-profile", "", "memory profiling output path")
	flag.Parse()
	if conf.StreamKey == "" {
		log.Fatal().Msg("stream key not set")
	}
	l, err := zerolog.ParseLevel(conf.LogLevel)
	if err != nil {
		log.Error().Err(err).Msg("parsing log level")
		os.Exit(1)
	}
	zerolog.SetGlobalLevel(l)
	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			log.Error().Err(err).Msg("creating cpu profile output")
			os.Exit(1)
		}
		// runtime.SetCPUProfileRate(250)
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
		defer f.Close()
	}
	ctx := context.Background()
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	colorChanSize := 15
	// color palette channel
	errorChannel := make(chan error, 5)
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

	ingestURL, err := twitch.IngestURL(ctx, httpClient, conf.StreamKey)
	if err != nil {
		log.Error().Err(err).Msg("getting ingest URL")
		os.Exit(1)
	}

	frameMaker := frame.LinearGradient{
		ColorChannel: colorChannel,
		Transition:   conf.FrameCount,
		Rect:         image.Rect(0, 0, conf.ImageWidth, conf.ImageHeight),
	}
	go frameMaker.Run()
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
		WithInput(&frameMaker).
		Output(outPath, ffmpeg.KwArgs{
			"framerate": 30,
			"c:v":       "libx264",
			"b:v":       "6000k",
			"preset":    "veryfast",
			"f":         "flv",
		}).
		OverWriteOutput().
		ErrorToStdOut().
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

	for {
		done := false
		select {
		case <-ctx.Done():
			stop()
			log.Info().Msg("shutting down")
			done = true
			if *cpuProfile != "" {
				pprof.StopCPUProfile()
			}
			if *memProfile != "" {
				memDump(*memProfile)
			}

		case err := <-errorChannel:
			log.Error().Err(err).Send()
			if errors.Is(err, errFfmpegExit) {
				stop()
				done = true
				if *cpuProfile != "" {
					pprof.StopCPUProfile()
				}
				if *memProfile != "" {
					memDump(*memProfile)
				}
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
