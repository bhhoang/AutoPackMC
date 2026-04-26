package logger

import (
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

var (
	once     sync.Once
	instance zerolog.Logger
)

// Init initializes the global logger with the given level.
func Init(level string) {
	once.Do(func() {
		lvl, err := zerolog.ParseLevel(level)
		if err != nil {
			lvl = zerolog.InfoLevel
		}
		output := zerolog.ConsoleWriter{
			Out:        os.Stderr,
			TimeFormat: time.RFC3339,
		}
		instance = zerolog.New(output).Level(lvl).With().Timestamp().Logger()
	})
}

// Get returns the global zerolog.Logger instance, initializing with Info level if needed.
func Get() *zerolog.Logger {
	once.Do(func() {
		output := zerolog.ConsoleWriter{
			Out:        os.Stderr,
			TimeFormat: time.RFC3339,
		}
		instance = zerolog.New(output).Level(zerolog.InfoLevel).With().Timestamp().Logger()
	})
	return &instance
}
