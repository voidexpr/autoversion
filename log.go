package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// ANSI color codes
const (
	colorReset   = "\033[0m"
	colorBold    = "\033[1m"
	colorDim     = "\033[2m"
	colorBoldRed = "\033[1;31m"
)

// Log files of a launchd job, size-capped when written with --log-dir
const (
	stdoutLogName    = "stdout.log"
	stderrLogName    = "stderr.log"
	logMaxSize       = 10 * 1024 * 1024
	logKeep          = 5
	logCheckInterval = time.Minute
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

// nowTS returns the current time in the log timestamp format.
func nowTS() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

// colorize wraps msg in an ANSI color when f is a terminal, so log files stay plain
func colorize(f *os.File, color, msg string) string {
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return msg
	}
	return color + msg + colorReset
}

func writeErrLog(msg string) {
	if errLogFile == nil {
		return
	}
	errLogMutex.Lock()
	defer errLogMutex.Unlock()
	fmt.Fprintf(errLogFile, "[%s] %s\n", nowTS(), msg)
}

func logError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, colorize(os.Stderr, colorBoldRed, fmt.Sprintf("[%s] %s", nowTS(), msg)))
	writeErrLog(msg)
}

// logSkip prints a skip message in dim/grey
func logSkip(format string, args ...interface{}) {
	fmt.Print(colorize(os.Stdout, colorDim, fmt.Sprintf(format, args...)))
}

// logGit prints a git-related message in bold
func logGit(format string, args ...interface{}) {
	fmt.Print(colorize(os.Stdout, colorBold, fmt.Sprintf(format, args...)))
}

// logFile redirects out (stdout or stderr) to path and rotates it by size
type logFile struct {
	out     *os.File
	path    string
	maxSize int64
	lastErr string
}

func rotatedLogPath(path string, n int) string {
	return fmt.Sprintf("%s.%d", path, n)
}

// open points out's file descriptor at path, so every write to out, including
// Go runtime panics on stderr, lands in the file
func (l *logFile) open() error {
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	return unix.Dup2(int(f.Fd()), int(l.out.Fd()))
}

// rotate renames path to path.1, path.1 to path.2, ... dropping path.<logKeep>
func (l *logFile) rotate() error {
	for i := logKeep - 1; i >= 1; i-- {
		if err := os.Rename(rotatedLogPath(l.path, i), rotatedLogPath(l.path, i+1)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.Rename(l.path, rotatedLogPath(l.path, 1))
}

// check rotates path when it reaches maxSize, and reopens it when it was
// removed or replaced (e.g. by install or by hand)
func (l *logFile) check() error {
	current, err := l.out.Stat()
	if err != nil {
		return err
	}
	onDisk, err := os.Stat(l.path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && os.SameFile(current, onDisk) {
		if current.Size() < l.maxSize {
			return nil
		}
		if err := l.rotate(); err != nil {
			return err
		}
	}
	return l.open()
}

// report logs err only when it differs from the previous one, so a persistent
// failure doesn't add a line every check
func (l *logFile) report(err error) {
	if err == nil {
		l.lastErr = ""
		return
	}
	if err.Error() != l.lastErr {
		logError("log file %s: %v", l.path, err)
	}
	l.lastErr = err.Error()
}

// redirectLogs sends stdout and stderr to dir/stdout.log and dir/stderr.log,
// rotated by size. Errors are logged and never stop the caller: output keeps
// going to wherever it went before.
func redirectLogs(dir string) {
	if err := EnsureDir(dir); err != nil {
		logError("failed to create log directory: %v", err)
		return
	}
	logs := []*logFile{
		{out: os.Stdout, path: filepath.Join(dir, stdoutLogName), maxSize: logMaxSize},
		{out: os.Stderr, path: filepath.Join(dir, stderrLogName), maxSize: logMaxSize},
	}
	for _, l := range logs {
		l.report(l.open())
	}
	go func() {
		for range time.Tick(logCheckInterval) {
			for _, l := range logs {
				l.report(l.check())
			}
		}
	}()
}

// removeLogs removes path and its rotated copies
func removeLogs(path string) error {
	paths := []string{path}
	for i := 1; i <= logKeep; i++ {
		paths = append(paths, rotatedLogPath(path, i))
	}
	var errs []error
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
