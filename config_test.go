package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bradleyjkemp/cupaloy/v2"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/tiiuae/mission-data-recorder/internal"
	std_msgs_msg "github.com/tiiuae/mission-data-recorder/msgs/std_msgs/msg"
	"github.com/tiiuae/rclgo/pkg/rclgo"
)

func readRecordings(dir string) ([]interface{}, error) {
	bags, err := filepath.Glob(filepath.Join(dir, "*", "*.db3"))
	if err != nil {
		return nil, err
	}
	var recordings []interface{}
	for _, bag := range bags {
		recordings = append(recordings, internal.ReadRosbag(bag)...)
	}
	return recordings, nil
}

func TestConfigUnmarshalYAML(t *testing.T) {
	data := []struct {
		in string
		c  *updatableConfig
		e  error
	}{
		{in: ``},
		{in: `topics:`},
		{in: `topics:
size_threshold: 15000000`},
		{in: `topics:  `},
		{in: `topics: ""`},
		{in: `topics: '*'
size_threshold: 16000000`},
		{in: `topics: alll`},
		{in: `topics:
  - /test_topic1
  - /test_topic2`},
		{in: `size_threshold: 16000000
extra_args:
topics:
  - /test_topic1
  - /test_topic2`},
		{in: `size_threshold: 16000000`},
		{in: `size_threshold: 16000000
non_existent_key:`},
		{in: `size_threshold: 16000000
non_existent_key:
extra_args: [arg1, arg2]`},
		{in: `max_upload_count: -1`},
		{in: `max_upload_count: 2.2`},
		{in: `max_upload_count: 7`},
		{in: `compression_mode: not supported`},
		{in: `compression_mode: gzip`},
	}
	for i := range data {
		data[i].c, data[i].e = parseUpdatableConfigYAML(data[i].in)
	}
	cupaloy.SnapshotT(t, data)
}

type fakeUploadManager struct {
	t *testing.T
}

func (m *fakeUploadManager) StartWorker(ctx context.Context) {
	m.t.Log("worker started")
}

func (m *fakeUploadManager) SetConfig(n int, mode compressionMode) {
	m.t.Log("worker count set to", n, "compression mode set to", mode)
}

func (m *fakeUploadManager) AddBag(ctx context.Context, bag *bagMetadata) {
	m.t.Log("got bag", bag.path)
}

func TestConfigWatcher(t *testing.T) {
	var (
		watcherStopped          = make(chan struct{})
		watcherCtx, stopWatcher = context.WithCancel(context.Background())

		rclctx                *rclgo.Context
		recorderNode          *rclgo.Node
		configPub, aPub, bPub *rclgo.Publisher

		watcher *configWatcher

		strMsg = func(s string) *std_msgs_msg.String {
			m := std_msgs_msg.NewString()
			m.Data = s
			return m
		}
	)
	const sleepTime = 5 * time.Second
	tempDir := t.TempDir()
	defer func() {
		if rclctx != nil {
			rclctx.Close()
		}
	}()
	Convey("Scenario: configWatcher manages a missionDataRecorder and restarts it when configuration changes", t, func() {
		Convey("Create publishers", func() {
			rclctx, err := rclgo.NewContext(0, nil)
			So(err, ShouldBeNil)
			recorderNode, err = rclctx.NewNode("mission_data_recorder", "/test")
			So(err, ShouldBeNil)
			configNode, err := rclctx.NewNode("config_node", "/test")
			So(err, ShouldBeNil)
			configPub, err = configNode.NewPublisher("mission_data_recorder/config", std_msgs_msg.StringTypeSupport, nil)
			So(err, ShouldBeNil)
			testNode, err := rclctx.NewNode("test_node", "/test")
			So(err, ShouldBeNil)
			aPub, err = testNode.NewPublisher("a", std_msgs_msg.StringTypeSupport, nil)
			So(err, ShouldBeNil)
			bPub, err = testNode.NewPublisher("b", std_msgs_msg.StringTypeSupport, nil)
			So(err, ShouldBeNil)
		})
		Convey("Start configWatcher", func() {
			diagnostics, err := newDiagnosticsMonitor(recorderNode)
			So(err, ShouldBeNil)
			watcher, err = newConfigWatcher(
				recorderNode,
				&missionDataRecorder{Dir: tempDir},
				&fakeUploadManager{t: t},
				diagnostics,
				&updatableConfig{
					SizeThreshold: defaultSizeThreshold,
					Topics:        topicList{Topics: []string{"/test/a"}},
				},
			)
			So(err, ShouldBeNil)
			go func() {
				defer close(watcherStopped)
				//nolint:errorlint // Wrapped errors are deliberately ignored.
				switch watcher.Run(watcherCtx) {
				case nil, context.Canceled:
				default:
					t.Error(err)
				}
			}()
			time.Sleep(2 * time.Second)
		})
		Convey("Recorder records data from topic a", func() {
			So(aPub.Publish(strMsg("a")), ShouldBeNil)
			time.Sleep(10 * time.Millisecond)
			So(bPub.Publish(strMsg("b")), ShouldBeNil)
			time.Sleep(sleepTime)
		})
		Convey("Recorder config is updated", func() {
			So(configPub.Publish(strMsg("topics: all")), ShouldBeNil)
			time.Sleep(sleepTime)
		})
		Convey("Recorder records correct data with updated config", func() {
			So(aPub.Publish(strMsg("a after update")), ShouldBeNil)
			time.Sleep(10 * time.Millisecond)
			So(bPub.Publish(strMsg("b after update")), ShouldBeNil)
			time.Sleep(sleepTime)
		})
		Convey("Recorder is stopped", func() {
			So(configPub.Publish(strMsg("topics:")), ShouldBeNil)
			time.Sleep(sleepTime)
		})
		Convey("Recorder doesn't record anything when stopped", func() {
			So(aPub.Publish(strMsg("a after stopping")), ShouldBeNil)
			time.Sleep(10 * time.Millisecond)
			So(bPub.Publish(strMsg("b after stopping")), ShouldBeNil)
			time.Sleep(sleepTime)
		})
		Convey("Recorder is started again", func() {
			So(configPub.Publish(strMsg(`topics: ["/test/b"]`)), ShouldBeNil)
			time.Sleep(sleepTime)
		})
		Convey("Recorder records data from topic b after starting again", func() {
			So(aPub.Publish(strMsg("a after starting again")), ShouldBeNil)
			time.Sleep(10 * time.Millisecond)
			So(bPub.Publish(strMsg("b after starting again")), ShouldBeNil)
			time.Sleep(sleepTime)
		})
		Convey("Stop recording", func() {
			stopWatcher()
			<-watcherStopped
		})
		Convey("Validate that recordings are correct", func() {
			recordings, err := readRecordings(tempDir)
			So(err, ShouldBeNil)
			So(cupaloy.SnapshotMulti(t.Name(), recordings...), ShouldBeNil)
		})
	})
}
