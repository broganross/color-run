# color-run
Silly little project I made up to generate animations and stream them to twitch.tv.

## Requirements
This uses ffmpeg to stream to twitch.tv so it will need to be installed.

## Configuration
You may use environment variables or command line arguments for configuration

| Env Var | Cmd Line | Default | Description |
| ------- | -------- | ------- | ----------- |
| COLORRUN_RANDOMMODEL | -r | False | If a daily color model should be chosen at random.  Otherwise use the default color model. |
| COLORRUN_IMAGEWIDTH | -w | 1920 | Width of the output video. |
| COLORRUN_IMAGEHEIGHT | -h | 1080 | Height of  the output video. |
| COLORRUN_FRAMECOUNT | -f | 90 | The number of frames it takes to transition from one color to another. |
| COLORRUN_STREAMKEY | -k | | [REQUIRED] Streaming key to use with Twitch.tv |
| COLORRUN_DUMPDIR | -d | | Directory to write video to instead of sending to Twitch.tv |
| COLORRUN_LOGLEVEL | -l | debug | Zerlog's logging level |

## Build & Run
Standard process applies:

```> go build -o main ./cmd/main.go```
```> ./main -k live_00000000```

