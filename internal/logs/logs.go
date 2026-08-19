// Package logs streams container and journald output.
package logs

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

// Line is one log line.
type Line struct {
	Text   string `json:"text"`
	Stream string `json:"stream"` // stdout | stderr | journal
	At     string `json:"at,omitempty"`
}

// Source identifies what to tail.
type Source struct {
	Kind   string // container | unit
	Target string
	Tail   int
}

// validTarget mirrors the action package: only names real containers and units
// can have. journalctl is executed without a shell, but a target that cannot
// name anything should be rejected before it becomes an argument.
func validTarget(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-' || r == '@':
		default:
			return false
		}
	}
	return true
}

// Stream tails a source until ctx is cancelled, sending lines to out.
// It closes out when the source ends.
func Stream(ctx context.Context, docker *client.Client, src Source, out chan<- Line) error {
	defer close(out)

	if !validTarget(src.Target) {
		return fmt.Errorf("%q is not a valid log source", src.Target)
	}
	if src.Tail <= 0 || src.Tail > 5000 {
		src.Tail = 200
	}

	switch src.Kind {
	case "container":
		return streamContainer(ctx, docker, src, out)
	case "unit":
		return streamUnit(ctx, src, out)
	}
	return fmt.Errorf("unknown log source kind %q", src.Kind)
}

func streamContainer(ctx context.Context, docker *client.Client, src Source, out chan<- Line) error {
	if docker == nil {
		return fmt.Errorf("the Docker daemon is not reachable")
	}

	// A container with a TTY emits one raw stream; without one the daemon
	// multiplexes stdout and stderr with per-frame headers. Reading the wrong
	// shape turns the log into binary noise, so ask first.
	tty := false
	if info, err := docker.ContainerInspect(ctx, src.Target, client.ContainerInspectOptions{}); err == nil {
		tty = info.Container.Config != nil && info.Container.Config.Tty
	}

	reader, err := docker.ContainerLogs(ctx, src.Target, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Timestamps: true,
		Tail:       strconv.Itoa(src.Tail),
	})
	if err != nil {
		return err
	}
	defer reader.Close()

	if tty {
		_, err = io.Copy(newLineWriter(ctx, out, "stdout"), reader)
	} else {
		stdout := newLineWriter(ctx, out, "stdout")
		stderr := newLineWriter(ctx, out, "stderr")
		_, err = stdcopy.StdCopy(stdout, stderr, reader)
	}
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func streamUnit(ctx context.Context, src Source, out chan<- Line) error {
	unit := src.Target
	if !strings.HasSuffix(unit, ".service") {
		unit += ".service"
	}

	// Arguments are passed directly to journalctl; no shell is involved.
	cmd := exec.CommandContext(ctx, "journalctl",
		"--unit", unit,
		"--lines", strconv.Itoa(src.Tail),
		"--follow",
		"--output", "short-iso",
		"--no-pager")

	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("journalctl: %w", err)
	}
	defer cmd.Wait()

	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil
		case out <- Line{Text: scanner.Text(), Stream: "journal"}:
		}
	}
	return nil
}

// lineWriter turns the byte stream from Docker into whole lines.
type lineWriter struct {
	ctx    context.Context
	out    chan<- Line
	stream string
	buf    []byte
}

func newLineWriter(ctx context.Context, out chan<- Line, stream string) *lineWriter {
	return &lineWriter{ctx: ctx, out: out, stream: stream}
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := strings.IndexByte(string(w.buf), '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(w.buf[:i]), "\r")
		w.buf = w.buf[i+1:]

		at, text := splitTimestamp(line)
		select {
		case <-w.ctx.Done():
			return len(p), w.ctx.Err()
		case w.out <- Line{Text: text, Stream: w.stream, At: at}:
		}
	}
	// A single line larger than this is a runaway, not a log.
	if len(w.buf) > 1024*1024 {
		w.buf = w.buf[:0]
	}
	return len(p), nil
}

// splitTimestamp peels the RFC3339 prefix Docker adds when Timestamps is set.
func splitTimestamp(line string) (at, text string) {
	space := strings.IndexByte(line, ' ')
	if space <= 0 {
		return "", line
	}
	if _, err := time.Parse(time.RFC3339Nano, line[:space]); err != nil {
		return "", line
	}
	return line[:space], line[space+1:]
}
