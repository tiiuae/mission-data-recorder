package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	std_msgs_msg "github.com/tiiuae/mission-data-recorder/msgs/std_msgs/msg"
	"github.com/tiiuae/rclgo/pkg/rclgo"
	"gopkg.in/yaml.v3"
)

type topicList struct {
	Topics []string
	All    bool
}

func (l *topicList) Type() string {
	return "topics"
}

//nolint:unparam // Implements the interface pflag.Value.
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

func (l *topicList) String() string {
	if l.All {
		return "*"
	}
	return strings.Join(l.Topics, ",")
}

func (l *topicList) UnmarshalYAML(val *yaml.Node) error {
	var decoded interface{}
	if err := val.Decode(&decoded); err != nil {
		return err
	}
	ts, err := l.Parse(decoded)
	if err != nil {
		return err
	}
	*l = ts.(topicList)
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
	sub        *rclgo.Subscription
	RetryDelay time.Duration

	recorder      *missionDataRecorder
	uploadManager uploadManagerInterface
	diagnostics   *diagnosticsMonitor

	nextConfig chan *updatableConfig

	// +checklocks:stopRecorderMutex
	stopRecorder      context.CancelFunc
	stopRecorderMutex sync.Mutex

	retryTimerActive bool
	retryTimer       *time.Timer
}

func newConfigWatcher(
	node *rclgo.Node,
	recorder *missionDataRecorder,
	uploadManager uploadManagerInterface,
	diagnostics *diagnosticsMonitor,
	initConfig *updatableConfig,
) (w *configWatcher, err error) {
	w = &configWatcher{
		RetryDelay:    5 * time.Second,
		recorder:      recorder,
		uploadManager: uploadManager,
		diagnostics:   diagnostics,

		nextConfig: make(chan *updatableConfig, 1),
	}
	w.retryTimer = time.NewTimer(w.RetryDelay)
	if !w.retryTimer.Stop() {
		<-w.retryTimer.C
	}
	w.nextConfig <- initConfig
	opts := rclgo.NewDefaultSubscriptionOptions()
	opts.Qos.Durability = rclgo.RmwQosDurabilityPolicyTransientLocal
	opts.Qos.Reliability = rclgo.RmwQosReliabilityPolicyReliable
	w.sub, err = node.NewSubscriptionWithOpts(
		"~/config",
		std_msgs_msg.StringTypeSupport,
		opts,
		w.onUpdate,
	)
	if err != nil {
		return nil, err
	}
	return w, nil
}

func (w *configWatcher) Close() error {
	if err := w.sub.Close(); err != nil {
		return fmt.Errorf("failed to close configWatcher: %w", err)
	}
	return nil
}

func (w *configWatcher) Run(ctx context.Context) error {
	var currentConfig *updatableConfig
	w.sub.Node().Logger().Info("starting mission-data-recorder")
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

func (w *configWatcher) startRecorder(ctx context.Context, config *updatableConfig) {
	startRecorder := w.applyConfig(config)
	ctx = w.newRecorderContext(ctx)
	w.uploadManager.StartWorker(ctx)
	if startRecorder {
		w.diagnostics.ReportSuccess("recorder", "running")
		err := w.recorder.Start(ctx, w.uploadManager.AddBag)
		//nolint:errorlint // Wrapped errors are deliberately ignored.
		switch err {
		case nil, context.Canceled:
		default:
			w.sub.Node().Logger().Errorf("recorder stopped with an error, trying again in %v: %v", w.RetryDelay, err)
			w.diagnostics.ReportError("recorder", "failed: ", err)
			w.retryTimerActive = true
			w.retryTimer.Reset(w.RetryDelay)
		}
	} else {
		w.diagnostics.ReportSuccess("recorder", "stopped")
	}
}

func (w *configWatcher) onUpdate(s *rclgo.Subscription) {
	var configYaml std_msgs_msg.String
	if _, err := s.TakeMessage(&configYaml); err != nil {
		w.sub.Node().Logger().Errorln("failed to read config from topic:", err)
		w.diagnostics.ReportError("config", err)
		return
	}
	config, err := parseUpdatableConfigYAML(configYaml.Data)
	if err != nil {
		w.sub.Node().Logger().Errorln("failed to parse config:", err)
		w.diagnostics.ReportError("config", err)
		return
	}
	w.sub.Node().Logger().Infoln("got new config:", configYaml.Data)
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
	defer w.diagnostics.ReportSuccess("config", "applied")
	w.uploadManager.SetConfig(config.MaxUploadCount, config.CompressionMode)
	w.recorder.SizeThreshold = config.SizeThreshold
	w.recorder.ExtraArgs = config.ExtraArgs
	if config.Topics.All {
		w.recorder.Topics = nil
		return true
	}
	w.recorder.Topics = config.Topics.Topics
	return len(config.Topics.Topics) != 0
}
