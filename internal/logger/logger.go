package logger

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"

	"github.com/kardianos/service"
)

type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
)

var currentLevel Level = INFO
var currentWriter io.Writer = os.Stderr

func GetWriter() io.Writer {
	return currentWriter
}

func SetLevel(levelStr string) {
	switch strings.ToUpper(levelStr) {
	case "DEBUG":
		currentLevel = DEBUG
	case "INFO":
		currentLevel = INFO
	case "WARN":
		currentLevel = WARN
	case "ERROR":
		currentLevel = ERROR
	default:
		currentLevel = INFO
	}
}

func Init(logFile string, levelStr string) error {
	SetLevel(levelStr)
	
	var writer io.Writer = os.Stderr
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("failed to open log file: %w", err)
		}
		if service.Interactive() {
			writer = io.MultiWriter(os.Stderr, f)
		} else {
			writer = f
		}
	}
	
	currentWriter = writer
	log.SetOutput(writer)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	// Configure slog to use the same writer
	var slogLevel slog.Level
	switch currentLevel {
	case DEBUG:
		slogLevel = slog.LevelDebug
	case INFO:
		slogLevel = slog.LevelInfo
	case WARN:
		slogLevel = slog.LevelWarn
	case ERROR:
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}

	handler := slog.NewTextHandler(writer, &slog.HandlerOptions{
		Level: slogLevel,
	})
	slog.SetDefault(slog.New(handler))

	return nil
}

func Debug(format string, v ...interface{}) {
	if currentLevel <= DEBUG {
		log.Printf("[DEBUG] "+format, v...)
	}
}

func Info(format string, v ...interface{}) {
	if currentLevel <= INFO {
		log.Printf("[INFO] "+format, v...)
	}
}

func Warn(format string, v ...interface{}) {
	if currentLevel <= WARN {
		log.Printf("[WARN] "+format, v...)
	}
}

func Error(format string, v ...interface{}) {
	if currentLevel <= ERROR {
		log.Printf("[ERROR] "+format, v...)
	}
}
