package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type sessionLog struct {
	path           string
	file           *os.File
	stdoutOriginal *os.File
	stderrOriginal *os.File
	stdoutReader   *os.File
	stdoutWriter   *os.File
	stderrReader   *os.File
	stderrWriter   *os.File
	wg             sync.WaitGroup
}

func startSessionLog(modpackPath string) (*sessionLog, error) {
	logDir := filepath.Join(modpackPath, outputDirectory, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}

	baseName := "session-" + time.Now().Format("20060102-150405.000") + fmt.Sprintf("-%d", os.Getpid())
	var file *os.File
	for attempt := 0; attempt < 1000; attempt++ {
		name := baseName + ".log"
		if attempt > 0 {
			name = fmt.Sprintf("%s-%03d.log", baseName, attempt)
		}
		var err error
		file, err = os.OpenFile(filepath.Join(logDir, name), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
		if err == nil {
			break
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("create session log: %w", err)
		}
	}
	if file == nil {
		return nil, fmt.Errorf("create session log: exhausted unique filename attempts")
	}

	log := &sessionLog{
		path:           file.Name(),
		file:           file,
		stdoutOriginal: os.Stdout,
		stderrOriginal: os.Stderr,
	}
	if err := log.redirect(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return log, nil
}

func (l *sessionLog) redirect() error {
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create stdout log pipe: %w", err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		return fmt.Errorf("create stderr log pipe: %w", err)
	}

	l.stdoutReader = stdoutReader
	l.stdoutWriter = stdoutWriter
	l.stderrReader = stderrReader
	l.stderrWriter = stderrWriter
	os.Stdout = stdoutWriter
	os.Stderr = stderrWriter

	l.wg.Add(2)
	go l.copyOutput(stdoutReader, l.stdoutOriginal)
	go l.copyOutput(stderrReader, l.stderrOriginal)
	return nil
}

func (l *sessionLog) copyOutput(reader *os.File, console *os.File) {
	defer l.wg.Done()
	_, _ = io.Copy(io.MultiWriter(console, l.file), reader)
}

func (l *sessionLog) writeError(err error) {
	_, _ = fmt.Fprintf(l.file, "Error: %v\n", err)
}

func (l *sessionLog) close() error {
	os.Stdout = l.stdoutOriginal
	os.Stderr = l.stderrOriginal

	_ = l.stdoutWriter.Close()
	_ = l.stderrWriter.Close()
	l.wg.Wait()
	_ = l.stdoutReader.Close()
	_ = l.stderrReader.Close()
	return l.file.Close()
}
