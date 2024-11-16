package logger

import (
	"math"
	"os"
	"sync"
	"time"

	"github.com/charmbracelet/log"
)

var once sync.Once

// LogLevel is a logging level.
type LogLevel int32

const (
	// DebugLevel is the debug level.
	DebugLevel LogLevel = -4
	// InfoLevel is the info level.
	InfoLevel LogLevel = 0
	// WarnLevel is the warn level.
	WarnLevel LogLevel = 4
	// ErrorLevel is the error level.
	ErrorLevel LogLevel = 8
	// FatalLevel is the fatal level.
	FatalLevel LogLevel = 12
	// noLevel is used with log.Print.
	noLevel LogLevel = math.MaxInt32
)

type logger struct {
	*log.Logger
}

var singleton *logger

func InitializeLogger(level LogLevel) error {
	once.Do(
		func() {
			l := log.NewWithOptions(os.Stderr, log.Options{
				ReportCaller:    true,
				ReportTimestamp: true,
				TimeFormat:      time.RFC3339,
				Prefix:          "go-callvis 🏎️ ",
			})
			l.SetLevel(log.Level(level))
			singleton = &logger{l}
		},
	)
	return nil
}

func LogDebug(msg string, args ...interface{}) {
	singleton.Debugf(msg, args...)
}

func LogInfo(msg string, args ...interface{}) {
	singleton.Infof(msg, args...)
}

func LogWarn(msg string, args ...interface{}) {
	singleton.Warnf(msg, args...)
}

func LogError(msg string, args ...interface{}) {
	singleton.Errorf(msg, args...)
}

func LogFatal(msg string, args ...interface{}) {
	singleton.Fatalf(msg, args...)
}
