package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/fsnotify/fsnotify"
)

type onBagReady = func(context.Context, *bagMetadata)

type missionDataRecorder struct {
	// If empty defaults to "ros2".
	ROSCommand string

	// List of topics to record. If empty or nil, all topics are recorded.
	Topics []string

	// After bag file size exceeds SizeThreshold bytes it is split. If
	// SizeThreshold is non-positive the file is never split.
	SizeThreshold int

	// Extra arguments passed to ros bag record command.
	ExtraArgs []string

	// Directory where bags will be stored. This field must not be empty.
	Dir string

	Logger logger

	// This is the subdirectory of Dir currently used by the recorder.
	currentDir string
}

func (r *missionDataRecorder) Start(ctx context.Context, onBagReady onBagReady) error {
	//#nosec G301 -- The directory doesn't contain secrets.
	if err := os.MkdirAll(r.Dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", r.Dir, err)
	}
	r.currentDir = filepath.Join(r.Dir, time.Now().UTC().Format(timeFormat))
	watcher, err := r.startWatcher(ctx, onBagReady)
	if err != nil {
		return fmt.Errorf("failed to start file watching: %w", err)
	}
	defer watcher.Close()
	cmd := r.newCommand(ctx)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start recorder: %w", err)
	}
	stopped := make(chan struct{}, 2)
	defer func() { stopped <- struct{}{} }()
	stopErr := make(chan error, 1)
	go func() {
		select {
		case <-stopped:
			if err := cmd.Process.Kill(); err != nil {
				r.Logger.Errorf("failed to kill recorder process: %v", err)
			}
			stopErr <- nil
		case <-ctx.Done():
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				if killErr := cmd.Process.Kill(); killErr != nil {
					r.Logger.Errorf("failed to kill recorder process: %v", killErr)
				}
				stopErr <- err
			} else {
				stopErr <- nil
			}
		}
	}()
	var exitErr *exec.ExitError
	err = cmd.Wait()
	if err != nil && !(errors.As(err, &exitErr) && exitErr.ExitCode() == 2) {
		return fmt.Errorf("an error occurred during recording: %w", err)
	}
	stopped <- struct{}{}
	if err := <-stopErr; err != nil {
		return fmt.Errorf("failed to stop recorder gracefully: %w", err)
	}
	return nil
}

func (r *missionDataRecorder) newCommand(ctx context.Context) *exec.Cmd {
	rosCmd := r.ROSCommand
	if rosCmd == "" {
		rosCmd = "ros2"
	}
	args := []string{"bag", "record", "--output", r.currentDir}
	if r.SizeThreshold > 0 {
		args = append(args, "--max-bag-size", strconv.Itoa(r.SizeThreshold))
	}
	args = append(args, r.ExtraArgs...)
	if len(r.Topics) == 0 {
		args = append(args, "--all")
	} else {
		args = append(args, "--")
		args = append(args, r.Topics...)
	}
	//#nosec G204 -- The command needs to be configurable.
	cmd := exec.CommandContext(ctx, rosCmd, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

func (r *missionDataRecorder) startWatcher(
	ctx context.Context, onBagReady onBagReady,
) (*fsnotify.Watcher, error) {
	// The watcher first watches the parent directory of r.currentDir to detect the
	// creation of r.currentDir by the ros2 bag record command. Then the parent is
	// unwatched and r.currentDir is added to the watchlist.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	go func() {
		cleanedDir := filepath.Clean(r.currentDir)
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Create == fsnotify.Create {
					if filepath.Clean(event.Name) == cleanedDir {
						r.logFileWatchErr(watcher.Remove(filepath.Dir(r.currentDir)))
						r.logFileWatchErr(watcher.Add(r.currentDir))
					} else {
						r.notifyIfBagReady(ctx, onBagReady, event.Name)
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				r.logFileWatchErr(err)
			case <-ctx.Done():
				return
			}
		}
	}()
	if err = watcher.Add(filepath.Dir(r.currentDir)); err != nil {
		return nil, err
	}
	return watcher, nil
}

func (r *missionDataRecorder) logFileWatchErr(err error) {
	if err != nil {
		r.Logger.Errorln("an error occurred during file watching:", err)
	}
}

func (r *missionDataRecorder) notifyIfBagReady(
	ctx context.Context, onBagReady onBagReady, bagPath string,
) {
	// A notification for bag number n means bag number n-1 is ready, because
	// the file creation notification is emitted when the bag is created and is
	// initially empty.
	if bag := newBagMetadata(bagPath, -1, true); bag != nil && bag.number >= 0 {
		go onBagReady(ctx, bag)
	}
}

type bagMetadata struct {
	path   string
	number int
	isNew  bool
	index  int
}

var bagNumberRegex = regexp.MustCompile(`^(.*)_(\d+)\.db3$`)

func newBagMetadata(path string, delta int, isNew bool) *bagMetadata {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	matches := bagNumberRegex.FindStringSubmatch(base)
	if matches == nil {
		return nil
	}
	bagNumber, err := strconv.Atoi(matches[2])
	if err != nil {
		// We don't return an error value since the regex should only match a
		// parsable integer. If a parsing error occurs, it is an error in the
		// regex.
		panic(err)
	}
	bagNumber += delta
	base = fmt.Sprintf("%s_%d.db3", matches[1], bagNumber)
	return &bagMetadata{
		path:   filepath.Join(dir, base),
		number: bagNumber,
		isNew:  isNew,
	}
}
