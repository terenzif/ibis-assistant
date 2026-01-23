package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
)

type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
)

var currentLevel Level = INFO

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

func Init(logFile string, level string) error {
	SetLevel(level)
	
	var writer io.Writer = os.Stderr
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("failed to open log file: %w", err)
		}
		writer = io.MultiWriter(os.Stderr, f)
	}
	
	log.SetOutput(writer)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
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
