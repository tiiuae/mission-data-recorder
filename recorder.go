package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/fsnotify/fsnotify"
)

type onBagReady = func(ctx context.Context, path string)

type missionDataRecorder struct {
	// If empty defaults to "ros2".
	ROSCommand string

	// List of topics to record. If empty or nil, all topics are recorded.
	Topics []string

	// After bag file size exceeds SizeThreshold bytes it is split. If
	// SizeThreshold is non-positive the file is never split.
	SizeThreshold int

	// Directory where bags will be stored. This field must not be empty.
	Dir string
}

func (r *missionDataRecorder) Start(ctx context.Context, onBagReady onBagReady) error {
	watcher, err := r.startWatcher(ctx, onBagReady)
	if err != nil {
		return fmt.Errorf("failed to start file watching: %w", err)
	}
	defer watcher.Close()
	cmd := r.newCommand()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start recorder: %w", err)
	}
	stopped := make(chan struct{}, 2)
	defer func() { stopped <- struct{}{} }()
	stopErr := make(chan error, 1)
	go func() {
		select {
		case <-stopped:
			cmd.Process.Kill()
			stopErr <- nil
		case <-ctx.Done():
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				cmd.Process.Kill()
				stopErr <- err
			} else {
				stopErr <- nil
			}
		}
	}()
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("an error occurred during recording: %w", err)
	}
	stopped <- struct{}{}
	if err := <-stopErr; err != nil {
		return fmt.Errorf("failed to stop recorder gracefully: %w", err)
	}
	return nil
}

func (r *missionDataRecorder) newCommand() *exec.Cmd {
	rosCmd := r.ROSCommand
	if rosCmd == "" {
		rosCmd = "ros2"
	}
	args := []string{"bag", "record", "--output", r.Dir}
	if r.SizeThreshold > 0 {
		args = append(args, "--max-bag-size", strconv.Itoa(r.SizeThreshold))
	}
	if len(r.Topics) == 0 {
		args = append(args, "--all")
	} else {
		args = append(args, "--")
		args = append(args, r.Topics...)
	}
	cmd := exec.Command(rosCmd, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd
}

func (r *missionDataRecorder) startWatcher(
	ctx context.Context, onBagReady onBagReady,
) (*fsnotify.Watcher, error) {
	// The watcher first watches the parent directory of r.Dir to detect the
	// creation of r.Dir by the ros2 bag record command. Then the parent is
	// unwatched and r.Dir is added to the watchlist.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	go func() {
		cleanedDir := filepath.Clean(r.Dir)
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Create == fsnotify.Create {
					if filepath.Clean(event.Name) == cleanedDir {
						logFileWatchErr(watcher.Remove(filepath.Dir(r.Dir)))
						logFileWatchErr(watcher.Add(r.Dir))
					} else {
						r.notifyIfBagReady(ctx, onBagReady, event.Name)
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logFileWatchErr(err)
			case <-ctx.Done():
				return
			}
		}
	}()
	if err = watcher.Add(filepath.Dir(r.Dir)); err != nil {
		return nil, err
	}
	return watcher, nil
}

func logFileWatchErr(err error) {
	if err != nil {
		log.Println("an error occured during file watching:", err)
	}
}

var bagNumberRegex = regexp.MustCompile(`^(.*)_(\d+).db3$`)

func (r *missionDataRecorder) notifyIfBagReady(
	ctx context.Context, onBagReady onBagReady, bagPath string,
) {
	matches := bagNumberRegex.FindStringSubmatch(bagPath)
	if matches == nil {
		return
	}
	bagNumber, err := strconv.Atoi(matches[2])
	if err != nil {
		// We don't return an error value since the regex should only match a
		// parsable integer. If a parsing error occurs, it is an error in the
		// regex.
		panic(err)
	}
	// A notification for bag number n means bag number n-1 is ready, because
	// the file creation notification is emitted when the bag is created and is
	// initially empty.
	if bagNumber > 0 {
		go onBagReady(ctx, fmt.Sprintf("%s_%d.db3", matches[1], bagNumber-1))
	}
}
