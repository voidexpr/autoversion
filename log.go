package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// ANSI color codes
const (
	colorReset   = "\033[0m"
	colorBold    = "\033[1m"
	colorDim     = "\033[2m"
	colorBoldRed = "\033[1;31m"
)

var (
	errLogFile  *os.File
	errLogMutex sync.Mutex
	errLogPath  string
)

// initErrLog opens the error log file for appending
func initErrLog(path string) error {
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open error log file: %v", err)
	}
	errLogFile = f
	errLogPath = path
	return nil
}

// closeErrLog closes the error log file
func closeErrLog() {
	if errLogFile != nil {
		errLogFile.Close()
		errLogFile = nil
	}
}

// writeErrLog writes a message to the error log file (if open)
func writeErrLog(msg string) {
	if errLogFile == nil {
		return
	}
	errLogMutex.Lock()
	defer errLogMutex.Unlock()
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(errLogFile, "[%s] %s\n", timestamp, msg)
}

// logError prints an error to stderr in bold red and writes to error log
func logError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "%s%s%s\n", colorBoldRed, msg, colorReset)
	writeErrLog(msg)
}

// logSkip prints a skip message in dim/grey
func logSkip(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("%s%s%s", colorDim, msg, colorReset)
}

// logGit prints a git-related message in bold
func logGit(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("%s%s%s", colorBold, msg, colorReset)
}
