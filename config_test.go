package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bradleyjkemp/cupaloy/v2"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/tiiuae/mission-data-recorder/internal"
	std_msgs_msg "github.com/tiiuae/rclgo-msgs/std_msgs/msg"
	"github.com/tiiuae/rclgo/pkg/rclgo"
	"gopkg.in/yaml.v3"
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
		c  config
		e  error
	}{
		{in: ``},
		{in: `topics:`},
		{in: `topics:
size-threshold: 15000000`},
		{in: `topics:  `},
		{in: `topics: ""`},
		{in: `topics: all
size-threshold: 16000000`},
		{in: `topics: alll`},
		{in: `topics:
  - /test_topic1
  - /test_topic2`},
		{in: `size-threshold: 16000000
extra-args:
topics:
  - /test_topic1
  - /test_topic2`},
		{in: `size-threshold: 16000000`},
		{in: `size-threshold: 16000000
non-existent-key:`},
		{in: `size-threshold: 16000000
non-existent-key:
extra-args: [arg1, arg2]`},
	}
	for i := range data {
		data[i].e = yaml.Unmarshal([]byte(data[i].in), &data[i].c)
	}
	cupaloy.SnapshotT(t, data)
}

func TestConfigWatcher(t *testing.T) {
	var (
		watcherStopped          = make(chan struct{})
		watcherCtx, stopWatcher = context.WithCancel(context.Background())

		rclctx                *rclgo.Context
		configPub, aPub, bPub *rclgo.Publisher

		watcher *configWatcher

		onBagReady = func(ctx context.Context, path string) {
			log.Println("got bag", path)
		}

		strMsg = func(s string) *std_msgs_msg.String {
			m := std_msgs_msg.NewString()
			m.Data = s
			return m
		}
	)
	tempDir, err := os.MkdirTemp("", "")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)
	defer func() {
		if rclctx != nil {
			rclctx.Close()
		}
	}()
	Convey("Scenario: configWatcher manages a missionDataRecorder and restarts it when configuration changes", t, func() {
		Convey("Create publishers", func() {
			rclctx, err = rclgo.NewContext(nil, 0, nil)
			So(err, ShouldBeNil)
			configNode, err := rclctx.NewNode("config_node", "/test")
			So(err, ShouldBeNil)
			configPub, err = configNode.NewPublisher("mission_data_recorder/config", std_msgs_msg.StringTypeSupport)
			So(err, ShouldBeNil)
			testNode, err := rclctx.NewNode("test_node", "/test")
			So(err, ShouldBeNil)
			aPub, err = testNode.NewPublisher("a", std_msgs_msg.StringTypeSupport)
			So(err, ShouldBeNil)
			bPub, err = testNode.NewPublisher("b", std_msgs_msg.StringTypeSupport)
			So(err, ShouldBeNil)
		})
		Convey("Start configWatcher", func() {
			watcher, err = newConfigWatcher(
				"/test",
				"mission_data_recorder",
				&config{
					SizeThreshold: defaultSizeThreshold,
					Topics:        []string{"/test/a"},
				},
				onBagReady,
			)
			So(err, ShouldBeNil)
			watcher.Recorder.Dir = tempDir
			go func() {
				defer close(watcherStopped)
				switch watcher.Start(watcherCtx) {
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
			time.Sleep(500 * time.Millisecond)
		})
		Convey("Recorder config is updated", func() {
			So(configPub.Publish(strMsg("topics: all")), ShouldBeNil)
			time.Sleep(500 * time.Millisecond)
		})
		Convey("Recorder records correct data with updated config", func() {
			So(aPub.Publish(strMsg("a after update")), ShouldBeNil)
			time.Sleep(10 * time.Millisecond)
			So(bPub.Publish(strMsg("b after update")), ShouldBeNil)
			time.Sleep(500 * time.Millisecond)
		})
		Convey("Recorder is stopped", func() {
			So(configPub.Publish(strMsg("topics:")), ShouldBeNil)
			time.Sleep(500 * time.Millisecond)
		})
		Convey("Recorder doesn't record anything when stopped", func() {
			So(aPub.Publish(strMsg("a after stopping")), ShouldBeNil)
			time.Sleep(10 * time.Millisecond)
			So(bPub.Publish(strMsg("b after stopping")), ShouldBeNil)
			time.Sleep(500 * time.Millisecond)
		})
		Convey("Recorder is started again", func() {
			So(configPub.Publish(strMsg(`topics: ["/test/b"]`)), ShouldBeNil)
			time.Sleep(500 * time.Millisecond)
		})
		Convey("Recorder records data from topic b after starting again", func() {
			So(aPub.Publish(strMsg("a after starting again")), ShouldBeNil)
			time.Sleep(10 * time.Millisecond)
			So(bPub.Publish(strMsg("b after starting again")), ShouldBeNil)
			time.Sleep(500 * time.Millisecond)
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
