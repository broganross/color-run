package config

import "github.com/rs/zerolog"

type Config struct {
	RandomModel bool `default:"false"`
	ImageWidth  int  `default:"1280"`
	ImageHeight int  `default:"720"`
	FrameCount  int  `default:"90"`
	StreamKey   string
	DumpDir     string
	LogLevel    zerolog.Level `default:"debug"`
}
