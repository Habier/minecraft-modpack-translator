package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"modpack-translator/internal/provider"
)

type applicationLog struct {
	translationPath string
	providerPath    string
	runID           string
	translationFile *os.File
	providerFile    *os.File
	human           *slog.Logger
	structured      *slog.Logger
	stdout          capturedOutput
	stderr          capturedOutput
	copyErrors      []error
	copyErrorsMu    sync.Mutex
	waitForCopies   func()
	closeOnce       sync.Once
	closeErr        error
}

type capturedOutput struct {
	original *os.File
	reader   *os.File
	writer   *os.File
}

type synchronizedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (w *synchronizedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}

func startApplicationLog(modpackPath string) (*applicationLog, error) {
	return startApplicationLogAt(modpackPath, time.Now().UTC(), os.Getpid())
}

func startApplicationLogAt(modpackPath string, startedAt time.Time, pid int) (*applicationLog, error) {
	logDir, err := filepath.Abs(filepath.Join(modpackPath, outputDirectory, "logs"))
	if err != nil {
		return nil, fmt.Errorf("resolve log directory: %w", err)
	}
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}

	baseRunID := startedAt.UTC().Format("20060102T150405.000Z") + fmt.Sprintf("-%d", pid)
	for attempt := 0; attempt < 1000; attempt++ {
		runID := baseRunID
		if attempt > 0 {
			runID = fmt.Sprintf("%s-%03d", baseRunID, attempt)
		}
		translationPath := filepath.Join(logDir, "translation-"+runID+".log")
		translationFile, err := os.OpenFile(translationPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return nil, fmt.Errorf("create translation log: %w", err)
		}

		providerPath := filepath.Join(logDir, "provider-failures-"+runID+".jsonl")
		providerFile, err := os.OpenFile(providerPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
		if err != nil {
			closeErr := translationFile.Close()
			removeErr := os.Remove(translationPath)
			if os.IsExist(err) && closeErr == nil && removeErr == nil {
				continue
			}
			return nil, errors.Join(fmt.Errorf("create provider diagnostics log: %w", err), closeErr, removeErr)
		}

		logWriter := &synchronizedWriter{w: translationFile}
		appLog := &applicationLog{
			translationPath: translationPath,
			providerPath:    providerPath,
			runID:           runID,
			translationFile: translationFile,
			providerFile:    providerFile,
			human: slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{
				ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
					if attr.Key == slog.LevelKey {
						return slog.Attr{}
					}
					return attr
				},
			})),
			structured: slog.New(slog.NewJSONHandler(providerFile, &slog.HandlerOptions{
				ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
					if attr.Key == slog.TimeKey {
						attr.Key = "timestamp"
					}
					return attr
				},
			})),
		}
		if err := appLog.captureProcessOutput(logWriter); err != nil {
			return nil, errors.Join(err, translationFile.Close(), providerFile.Close(), os.Remove(translationPath), os.Remove(providerPath))
		}
		return appLog, nil
	}
	return nil, errors.New("create run logs: exhausted unique filename attempts")
}

func (l *applicationLog) reportPaths(w io.Writer) {
	fmt.Fprintf(w, "Session log: %s\nProvider diagnostics: %s\n", l.translationPath, l.providerPath)
}

func (l *applicationLog) providerBuildOptions(debug bool) provider.BuildOptions {
	if !debug {
		return provider.BuildOptions{}
	}
	return provider.BuildOptions{
		Debug:                              true,
		InvalidResponseQuarantineDirectory: filepath.Join(filepath.Dir(l.providerPath), "invalid-provider-responses"),
		RunID:                              l.runID,
	}
}

func (l *applicationLog) Event(ctx context.Context, event string, attrs ...slog.Attr) {
	l.human.LogAttrs(ctx, slog.LevelInfo, event, attrs...)
}

func (l *applicationLog) ProviderFailure(ctx context.Context, d provider.Diagnostic) {
	l.structured.LogAttrs(ctx, slog.LevelError, "provider_failure",
		slog.String("schema", "modpack-translator.provider-failure/v1"),
		slog.String("run_id", l.runID), slog.String("provider", d.Provider), slog.String("model", d.Model),
		slog.Int("http_status", d.HTTPStatus), slog.String("kind", string(d.Kind)),
		slog.String("provider_code", d.Code), slog.Bool("details_omitted", d.DetailsOmitted),
		slog.Int("batch_size", d.BatchSize), slog.Int("attempt", d.Attempt),
		slog.String("transition_target", d.TransitionTarget))
}

func (l *applicationLog) captureProcessOutput(logWriter io.Writer) error {
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("capture stdout: %w", err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		return errors.Join(fmt.Errorf("capture stderr: %w", err), stdoutReader.Close(), stdoutWriter.Close())
	}

	l.stdout = capturedOutput{original: os.Stdout, reader: stdoutReader, writer: stdoutWriter}
	l.stderr = capturedOutput{original: os.Stderr, reader: stderrReader, writer: stderrWriter}
	os.Stdout = stdoutWriter
	os.Stderr = stderrWriter

	var copies sync.WaitGroup
	copies.Add(2)
	copyOutput := func(output capturedOutput) {
		defer copies.Done()
		if _, err := io.Copy(io.MultiWriter(output.original, logWriter), output.reader); err != nil {
			l.copyErrorsMu.Lock()
			l.copyErrors = append(l.copyErrors, err)
			l.copyErrorsMu.Unlock()
		}
	}
	go copyOutput(l.stdout)
	go copyOutput(l.stderr)

	// Waiting is stored as a closure so close can drain both pipes before closing
	// the shared session file without exposing synchronization outside this type.
	l.waitForCopies = copies.Wait
	return nil
}

func (l *applicationLog) close() error {
	l.closeOnce.Do(func() {
		os.Stdout = l.stdout.original
		os.Stderr = l.stderr.original
		stdoutErr := l.stdout.writer.Close()
		stderrErr := l.stderr.writer.Close()
		l.waitForCopies()
		l.copyErrorsMu.Lock()
		copyErrors := append([]error(nil), l.copyErrors...)
		l.copyErrorsMu.Unlock()
		l.closeErr = errors.Join(stdoutErr, stderrErr, errors.Join(copyErrors...), l.stdout.reader.Close(), l.stderr.reader.Close(), l.translationFile.Close(), l.providerFile.Close())
	})
	return l.closeErr
}

type applicationLogCloser interface {
	close() error
}

func propagateApplicationLogClose(result error, closer applicationLogCloser) error {
	if closeErr := closer.close(); closeErr != nil {
		return errors.Join(result, fmt.Errorf("close application logs: %w", closeErr))
	}
	return result
}
