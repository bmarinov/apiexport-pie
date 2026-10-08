// Package watch keeps a diagram file in step with the manifests it is drawn from, while a viewer
// shows it.
package watch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Options configures Run.
type Options struct {
	// File is where the diagram is written for the Viewer to read.
	File string
	// Interval is how often the diagram is drawn again.
	Interval time.Duration
	// Generate draws the diagram, with notes to report alongside it.
	Generate func() (diagram []byte, notes []string, err error)
	// Viewer is started once File exists, and interrupted when Run's context ends.
	Viewer *exec.Cmd
	// Report receives the notes of each new diagram, and why drawing one failed.
	Report func(line string)
}

// Run writes the diagram to File, starts the Viewer, then draws the diagram every Interval and
// replaces File only when it changed. When drawing fails, File keeps the last diagram: manifests
// are often caught half-saved. Run returns nil once ctx ends and the Viewer has stopped, and an
// error if the Viewer exits on its own.
func Run(ctx context.Context, o Options) error {
	var last []byte
	written := false
	refresh := func() error {
		diagram, notes, err := o.Generate()
		if err != nil {
			return err
		}
		if written && bytes.Equal(diagram, last) {
			return nil
		}
		if err := ReplaceFile(o.File, diagram); err != nil {
			return err
		}
		last, written = diagram, true
		for _, note := range notes {
			o.Report(note)
		}
		return nil
	}

	if err := refresh(); err != nil {
		return err
	}
	if err := o.Viewer.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", o.Viewer.Path, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- o.Viewer.Wait() }()

	ticker := time.NewTicker(o.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := o.Viewer.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
				return fmt.Errorf("interrupting %s: %w", o.Viewer.Path, err)
			}
			<-exited
			return nil
		case err := <-exited:
			// Ctrl-C in a terminal reaches the Viewer too, and it may stop before ctx ends.
			if err == nil || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("%s exited: %w", o.Viewer.Path, err)
		case <-ticker.C:
			if err := refresh(); err != nil {
				o.Report("keeping the last diagram: " + err.Error())
			}
		}
	}
}

// ReplaceFile writes data to a temporary file next to file and renames it over file, so a reader
// such as `d2 --watch` never sees half a diagram.
func ReplaceFile(file string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(file), "."+filepath.Base(file)+".*")
	if err != nil {
		return fmt.Errorf("replacing %s: %w", file, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.Remove(tmp.Name()))
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return errors.Join(fmt.Errorf("replacing %s: %w", file, err), tmp.Close())
	}
	// CreateTemp makes the file private; a diagram is not.
	if err := tmp.Chmod(0o644); err != nil {
		return errors.Join(fmt.Errorf("replacing %s: %w", file, err), tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("replacing %s: %w", file, err)
	}
	if err := os.Rename(tmp.Name(), file); err != nil {
		return fmt.Errorf("replacing %s: %w", file, err)
	}
	return nil
}
