package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	stdimage "image"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/broganross/color-run/internal/colormind"
	"github.com/broganross/color-run/internal/image"
	"github.com/nicklaw5/helix/v2"
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
	Channel chan *image.SequenceFrame
	Ctx     context.Context
	cur     *image.SequenceFrame
	idx     int
}

func (fi *ffmpegInput) Read(p []byte) (int, error) {
	// TODO: add time out, and signal cancellation handling
	cnt := 0
	l := len(p)
	for {
		if fi.cur == nil {
			frame, ok := <-fi.Channel
			if !ok {
				// not sure if this is the appropriate error to return
				return cnt, io.EOF
			}
			fi.cur = frame
		}
		n := copy(p[cnt:], fi.cur.Image[fi.idx:])
		fi.idx += n
		cnt += n
		if fi.idx >= len(fi.cur.Image) {
			fi.cur = nil
			fi.idx = 0
		}
		if cnt >= l {
			break
		}
	}
	return cnt, nil
}

func rawBytesImage(w io.Writer, m stdimage.Image) error {
	// written size: 3686400
	size := m.Bounds()
	for y := size.Min.Y; y < size.Max.Y; y++ {
		for x := size.Min.X; x < size.Max.X; x++ {
			r, g, b, a := m.At(x, y).RGBA()
			_, err := w.Write([]byte{byte(r), byte(g), byte(b), byte(a)})
			if err != nil {
				return fmt.Errorf("writing raw bytes image: %w", err)
			}
		}
	}
	return nil
}

func main() {
	outPath := flag.String("o", "./", "output path of the image")
	_ = outPath
	width := flag.Int("w", 1280, "image width")
	height := flag.Int("h", 720, "image height")
	transitionFrames := flag.Uint("f", 90, "number of frames to transition from one color to another")
	twitchUser := flag.String("u", "", "twitch user name")
	twitchClientID := flag.String("c", "", "twitch client ID")
	twitchToken := flag.String("t", "", "twitch user access token")
	twitchRefresh := flag.String("r", "", "twitch refresh token")
	flag.Parse()
	zerolog.SetGlobalLevel(zerolog.DebugLevel)

	ctx := context.Background()
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// creates the color mind client and retrieves a random color palette
	cm := colormind.New()
	cm.Ctx = ctx
	cm.Client.Timeout = 5 * time.Second

	opts := &image.ProducerOptions{
		ColorMind:        cm,
		ImageRect:        stdimage.Rect(0, 0, *width, *height),
		TransitionFrames: int(*transitionFrames),
		ImageEncoder:     rawBytesImage,
	}
	imageMaker := image.NewProducer(opts)
	go imageMaker.Start()

	// Setup the Twitch stream
	twitchClient, err := helix.NewClient(&helix.Options{
		ClientID:        *twitchClientID,
		UserAccessToken: *twitchToken,
		RefreshToken:    *twitchRefresh,
	})
	if err != nil {
		log.Error().Err(err).Msg("making twitch client")
		os.Exit(1)
	}
	userResp, err := twitchClient.GetUsers(&helix.UsersParams{Logins: []string{*twitchUser}})
	if err != nil {
		log.Error().Err(err).Msg("getting user ID")
		os.Exit(1)
	} else if userResp.ResponseCommon.StatusCode < http.StatusOK || userResp.ResponseCommon.StatusCode > http.StatusIMUsed {
		err := fmt.Errorf("%s (%d): %s",
			userResp.ResponseCommon.Error,
			userResp.ResponseCommon.ErrorStatus,
			userResp.ResponseCommon.ErrorMessage,
		)
		log.Error().Err(err).Msg("getting user ID")
		os.Exit(1)
	}
	userID := userResp.Data.Users[0].ID

	resp, err := twitchClient.GetStreamKey(&helix.StreamKeyParams{
		BroadcasterID: userID,
	})
	if err != nil {
		log.Error().Err(err).Msg("getting stream key")
		os.Exit(1)
	} else if resp.ResponseCommon.StatusCode < http.StatusOK || resp.ResponseCommon.StatusCode > http.StatusIMUsed {
		err := fmt.Errorf("%s (%d): %s",
			resp.ResponseCommon.Error,
			resp.ResponseCommon.ErrorStatus,
			resp.ResponseCommon.ErrorMessage,
		)
		log.Error().Err(err).Msg("getting stream key")
		os.Exit(1)
	}
	streamKey := resp.Data.Data[0].StreamKey
	ingestResp, err := cm.Client.Get("https://ingest.twitch.tv/ingests")
	if err != nil {
		log.Error().Err(err).Msg("getting ingests")
		os.Exit(1)
	} else if ingestResp.StatusCode < http.StatusOK || ingestResp.StatusCode > http.StatusIMUsed {
		defer ingestResp.Body.Close()
		b, err := io.ReadAll(ingestResp.Body)
		if err != nil {
			log.Error().Err(err).Msg("reading ingest response body")
		}
		err = fmt.Errorf("getting ingest (%s): %s", http.StatusText(ingestResp.StatusCode), string(b))
		log.Error().Err(err).Msg("getting ingests")
		os.Exit(1)
	}
	defer ingestResp.Body.Close()
	r := ingestsResponse{}
	if err := json.NewDecoder(ingestResp.Body).Decode(&r); err != nil {
		log.Error().Err(err).Msg("decoding ingest response body")
		os.Exit(1)
	}
	// find default?
	var ingestURL string
	for _, i := range r.Ingests {
		if i.Default {
			ingestURL = i.URLTemplate
		}
	}
	if ingestURL == "" {
		log.Error().Msg("no default ingest server found")
		os.Exit(1)
	}
	ingestURL = strings.Replace(ingestURL, "{stream_key}", streamKey, -1)
	input := &ffmpegInput{Ctx: ctx, Channel: imageMaker.ImageChannel}
	proc := ffmpeg.Input("pipe:0", ffmpeg.KwArgs{
		// "framerate":                   30,
		"f":          "rawvideo",
		"pix_fmt":    "rgba",
		"video_size": fmt.Sprintf("%dx%d", *width, *height),
		// "use_wallclock_as_timestamps": "1",
	}).
		WithInput(input).
		Output(ingestURL, ffmpeg.KwArgs{
			"framerate": 30,
			"c:v":       "libx264",
			"preset":    "veryfast",
			// "b:v":       "6000k",
			// "maxrate":   "6000k",
			// "bufsize": "6000k",
			"f": "flv",
		}).
		SetFfmpegPath("C:\\Program Files\\ffmpeg\\bin\\ffmpeg.exe").
		ErrorToStdOut()
	ffmpegErrChan := make(chan error, 1)
	go func() {
		log.Debug().Msg("starting ffmpeg process")
		err = proc.Run()
		if err != nil {
			ffmpegErrChan <- err
		}
	}()

	// -re read native frame rate
	// -c:v video codec i assume
	// -b:v 6000k video bitrate?
	// ffmpeg -re -i "YourFile.mkv"
	// -c:v libx264 -preset veryfast -b:v 6000k -maxrate 6000k -bufsize 6000k -pix_fmt yuv420p -g 50 -c:a aac -b:a 160k -ac 2 -ar 44100 -f flv rtmp://live-ber.twitch.tv/app/live_XXXXX_XXXXXXXXXXXXXXXXXXXXXXXXXXXXX

	for {
		done := false
		select {
		case <-ctx.Done():
			imageMaker.Stop()
			stop()
			log.Info().Msg("shutting down")
			done = true
		case err := <-imageMaker.ErrorChannel:
			log.Error().Err(err).Msg("image generator")
		case err := <-ffmpegErrChan:
			log.Error().Err(err).Msg("running ffmpeg")
			imageMaker.Stop()
			stop()
			done = true
			// case frame := <-imageMaker.ImageChannel:
			// 	// log.Debug().Int("frame", frame.Frame).Msg("got frame")
			// 	if err := writeFrame(frame, *outPath); err != nil {
			// 		log.Error().Err(err).Msg("writing to disk")
			// 	}
		}
		if done {
			break
		}
	}
	os.Exit(0)
}

func writeFrame(img *image.SequenceFrame, outDir string) error {
	fileName := filepath.Join(outDir, fmt.Sprintf("%06d.ppm", img.Frame))
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
