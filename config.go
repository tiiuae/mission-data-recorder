package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	std_msgs_msg "github.com/tiiuae/mission-data-recorder/msgs/std_msgs/msg"
	"github.com/tiiuae/rclgo/pkg/rclgo"
	"gopkg.in/yaml.v3"
)

func onErr(err *error, f func() error) {
	if *err != nil {
		f()
	}
}

type topicList struct {
	Topics []string
	All    bool
}

func (l *topicList) Type() string {
	return "topics"
}

func (l *topicList) Set(val string) error {
	switch val {
	case "":
		l.All = false
		l.Topics = nil
	case "*":
		l.All = true
		l.Topics = nil
	default:
		l.All = false
		l.Topics = parseCommaSeparatedList(val)
	}
	return nil
}

func (l *topicList) Parse(val interface{}) (interface{}, error) {
	const errMsg = "'topics' must be an empty string, '*' or a list of strings"
	switch topics := val.(type) {
	case nil:
		return topicList{}, nil
	case string:
		var tl topicList
		if err := tl.Set(topics); err != nil {
			return nil, err
		}
		return tl, nil
	case []interface{}:
		var list topicList
		for _, topic := range topics {
			if topic, ok := topic.(string); ok {
				list.Topics = append(list.Topics, topic)
			} else {
				return nil, errors.New(errMsg)
			}
		}
		return list, nil
	}
	return nil, errors.New(errMsg)
}

func (s *topicList) String() string {
	if s.All {
		return "*"
	}
	return strings.Join(s.Topics, ",")
}

func (s *topicList) UnmarshalYAML(val *yaml.Node) error {
	var decoded interface{}
	if err := val.Decode(&decoded); err != nil {
		return err
	}
	ts, err := s.Parse(decoded)
	if err != nil {
		return err
	}
	*s = ts.(topicList)
	return nil
}

type updatableConfig struct {
	Topics          topicList       `yaml:"topics"`
	SizeThreshold   int             `yaml:"size_threshold"`
	ExtraArgs       []string        `yaml:"extra_args"`
	MaxUploadCount  int             `yaml:"max_upload_count"`
	CompressionMode compressionMode `yaml:"compression_mode"`
}

func parseUpdatableConfigYAML(s string) (*updatableConfig, error) {
	config := updatableConfig{
		SizeThreshold:   defaultSizeThreshold,
		MaxUploadCount:  defaultMaxUploadCount,
		CompressionMode: defaultCompressionMode,
	}
	if err := yaml.Unmarshal([]byte(s), &config); err != nil {
		return nil, err
	}
	if config.MaxUploadCount < 0 {
		return nil, errors.New("'max-upload-count' must be non-negative")
	}
	return &config, nil
}

type uploadManagerInterface interface {
	StartWorker(context.Context)
	SetConfig(int, compressionMode)
	AddBag(context.Context, *bagMetadata)
}

type configWatcher struct {
	*rclgo.Node
	RetryDelay time.Duration

	Recorder      missionDataRecorder
	UploadManager uploadManagerInterface

	nextConfig chan *updatableConfig

	stopRecorder      context.CancelFunc
	stopRecorderMutex sync.Mutex

	retryTimerActive bool
	retryTimer       *time.Timer
}

func newConfigWatcher(
	ns, nodeName string,
	initConfig *updatableConfig,
	ctx *rclgo.Context,
) (w *configWatcher, err error) {
	w = &configWatcher{
		RetryDelay: 5 * time.Second,
		nextConfig: make(chan *updatableConfig, 1),
	}
	w.retryTimer = time.NewTimer(w.RetryDelay)
	if !w.retryTimer.Stop() {
		<-w.retryTimer.C
	}
	w.nextConfig <- initConfig
	w.Node, err = ctx.NewNode(nodeName, ns)
	if err != nil {
		return nil, err
	}
	defer onErr(&err, w.Node.Close)
	_, err = w.Node.NewSubscription(
		"~/config",
		std_msgs_msg.StringTypeSupport,
		w.onUpdate,
	)
	if err != nil {
		return nil, err
	}
	return w, nil
}

func (w *configWatcher) Start(ctx context.Context) error {
	errs := make(chan error, 1)
	go func() {
		defer close(errs)
		errs <- w.Spin(ctx)
	}()
	var currentConfig *updatableConfig
	w.Logger().Info("starting mission-data-recorder")
	for {
		select {
		case <-ctx.Done():
			if err := <-errs; err != nil {
				return err
			}
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

func (w *configWatcher) startRecorder(ctx context.Context, config *updatableConfig) {
	startRecorder := w.applyConfig(config)
	ctx = w.newRecorderContext(ctx)
	w.UploadManager.StartWorker(ctx)
	if startRecorder {
		err := w.Recorder.Start(ctx, w.UploadManager.AddBag)
		switch err {
		case nil, context.Canceled:
		default:
			w.Logger().Errorf("recorder stopped with an error, trying again in %v: %v", w.RetryDelay, err)
			w.retryTimerActive = true
			w.retryTimer.Reset(w.RetryDelay)
		}
	}
}

func (w *configWatcher) onUpdate(s *rclgo.Subscription) {
	var configYaml std_msgs_msg.String
	if _, err := s.TakeMessage(&configYaml); err != nil {
		w.Logger().Errorln("failed to read config from topic:", err)
		return
	}
	config, err := parseUpdatableConfigYAML(configYaml.Data)
	if err != nil {
		w.Logger().Errorln("failed to parse config:", err)
		return
	}
	w.Logger().Infoln("got new config:", configYaml.Data)
	w.stopRecording()
	w.nextConfig <- config
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

func (w *configWatcher) applyConfig(config *updatableConfig) (startRecorder bool) {
	w.UploadManager.SetConfig(config.MaxUploadCount, config.CompressionMode)
	w.Recorder.SizeThreshold = config.SizeThreshold
	w.Recorder.ExtraArgs = config.ExtraArgs
	if config.Topics.All {
		w.Recorder.Topics = nil
		return true
	}
	w.Recorder.Topics = config.Topics.Topics
	return len(config.Topics.Topics) != 0
}
