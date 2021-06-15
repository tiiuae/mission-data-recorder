package main

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	std_msgs_msg "github.com/tiiuae/rclgo-msgs/std_msgs/msg"
	"github.com/tiiuae/rclgo/pkg/rclgo"
	"gopkg.in/yaml.v3"
)

func onErr(err *error, f func() error) {
	if *err != nil {
		f()
	}
}

type config struct {
	Topics          []string
	RecordAllTopics bool
	SizeThreshold   int
}

func (c *config) UnmarshalYAML(val *yaml.Node) error {
	var data map[string]interface{}
	if err := val.Decode(&data); err != nil {
		return err
	}
	switch x := data["size-threshold"].(type) {
	case nil:
		c.SizeThreshold = defaultSizeThreshold
	case int:
		c.SizeThreshold = x
	default:
		return errors.New("'size-threshold' must be an integer")
	}
	c.Topics = nil
	c.RecordAllTopics = false
	switch x := data["topics"].(type) {
	case nil:
	case string:
		switch x {
		case "":
		case "all":
			c.RecordAllTopics = true
		default:
			return errors.New("'topics' must be an empty string, the string 'all' or a list of strings")
		}
	case []interface{}:
		for _, t := range x {
			if s, ok := t.(string); ok {
				c.Topics = append(c.Topics, s)
			} else {
				return errors.New("'topics' must be an empty string, the string 'all' or a list of strings")
			}
		}
	default:
		return errors.New("'topics' must be an empty string, the string 'all' or a list of strings")
	}
	return nil
}

type configWatcher struct {
	RetryDelay time.Duration

	Recorder   missionDataRecorder
	onBagReady onBagReady

	nextConfig chan *config

	rclctx *rclgo.Context
	ws     *rclgo.WaitSet

	stopRecorder      context.CancelFunc
	stopRecorderMutex sync.Mutex

	retryTimerActive bool
	retryTimer       *time.Timer
}

func newConfigWatcher(
	ns, nodeName string,
	initConfig *config,
	onBagReady onBagReady,
) (w *configWatcher, err error) {
	w = &configWatcher{
		RetryDelay: 5 * time.Second,
		nextConfig: make(chan *config, 1),
		onBagReady: onBagReady,
	}
	w.retryTimer = time.NewTimer(w.RetryDelay)
	if !w.retryTimer.Stop() {
		<-w.retryTimer.C
	}
	w.nextConfig <- initConfig
	w.rclctx, err = rclgo.NewContext(nil, 0, nil)
	if err != nil {
		return nil, err
	}
	defer onErr(&err, w.rclctx.Close)
	node, err := w.rclctx.NewNode(nodeName, ns)
	if err != nil {
		return nil, err
	}
	sub, err := node.NewSubscription(
		nodeName+"/config",
		std_msgs_msg.StringTypeSupport,
		w.onUpdate,
	)
	if err != nil {
		return nil, err
	}
	w.ws, err = w.rclctx.NewWaitSet(500 * time.Millisecond)
	if err != nil {
		return nil, err
	}
	w.ws.AddSubscriptions(sub)
	return w, nil
}

func (w *configWatcher) Close() error {
	return w.rclctx.Close()
}

func (w *configWatcher) Start(ctx context.Context) error {
	w.ws.RunGoroutine(ctx)
	var currentConfig *config
	log.Println("starting mission-data-recorder")
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.retryTimer.C:
			w.retryTimerActive = false
			w.startRecorder(ctx, currentConfig)
		case currentConfig = <-w.nextConfig:
			if w.retryTimerActive && !w.retryTimer.Stop() {
				<-w.retryTimer.C
			}
			w.retryTimerActive = false
			w.startRecorder(ctx, currentConfig)
		}
	}
}

func (w *configWatcher) startRecorder(ctx context.Context, config *config) {
	if w.applyConfig(config) {
		err := w.Recorder.Start(w.newRecorderContext(ctx), w.onBagReady)
		switch err {
		case nil, context.Canceled:
		default:
			log.Printf("recorder stopped with an error, trying again in %v: %v", w.RetryDelay, err)
			w.retryTimerActive = true
			w.retryTimer.Reset(w.RetryDelay)
		}
	}
}

func (w *configWatcher) onUpdate(s *rclgo.Subscription) {
	var configYaml std_msgs_msg.String
	if _, err := s.TakeMessage(&configYaml); err != nil {
		log.Println("failed to read config from topic:", err)
		return
	}
	var config config
	if err := yaml.Unmarshal([]byte(configYaml.Data), &config); err != nil {
		log.Println("failed to parse config:", err)
		return
	}
	log.Println("got new config")
	w.stopRecording()
	w.nextConfig <- &config
}

func (w *configWatcher) newRecorderContext(ctx context.Context) (rctx context.Context) {
	w.stopRecorderMutex.Lock()
	defer w.stopRecorderMutex.Unlock()
	rctx, w.stopRecorder = context.WithCancel(ctx)
	return rctx
}

func (w *configWatcher) stopRecording() {
	w.stopRecorderMutex.Lock()
	defer w.stopRecorderMutex.Unlock()
	if w.stopRecorder != nil {
		w.stopRecorder()
	}
}

func (w *configWatcher) applyConfig(config *config) (startRecorder bool) {
	w.Recorder.SizeThreshold = config.SizeThreshold
	if config.RecordAllTopics {
		w.Recorder.Topics = nil
	} else {
		w.Recorder.Topics = config.Topics
		if len(config.Topics) == 0 {
			return false
		}
	}
	return true
}
