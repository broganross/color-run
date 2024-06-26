package config

type Config struct {
	RandomModel bool `default:"false"`
	ImageWidth  int  `default:"1920"`
	ImageHeight int  `default:"1080"`
	FrameCount  int  `default:"90"`
	StreamKey   string
	DumpDir     string
	LogLevel    string `default:"debug"`
}
