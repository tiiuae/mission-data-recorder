package main

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

type fakeUploader struct {
	t        *testing.T
	bagCount int
	bags     bagQueue
	done     chan struct{}
	mutex    sync.Mutex
}

func (u *fakeUploader) WithCompression(mode compressionMode) uploaderInterface {
	u.t.Log("compression mode set to", mode)
	return u
}

func (u *fakeUploader) UploadBag(ctx context.Context, bag *bagMetadata) error {
	time.Sleep(500 * time.Millisecond)
	u.mutex.Lock()
	defer u.mutex.Unlock()
	u.bags = append(u.bags, bag)
	if len(u.bags) == u.bagCount {
		close(u.done)
	}
	return nil
}

type fakeLogger struct{}

func (l fakeLogger) Infof(string, ...interface{}) error  { return nil }
func (l fakeLogger) Errorf(string, ...interface{}) error { return nil }
func (l fakeLogger) Errorln(...interface{}) error        { return nil }

func TestUploadManager(t *testing.T) {
	const workerCount = 5
	var (
		uploader = fakeUploader{
			t:        t,
			bagCount: 100,
			done:     make(chan struct{}),
		}
		uploadMan = newUploadManager(workerCount, &uploader, fakeLogger{})
		ctx       = context.Background()
		//#nosec G404 -- Tests should be deterministic.
		rnd = rand.New(rand.NewSource(42))
	)
	Convey("Scenario: uploadManager works correctly", t, func() {
		Convey("The correct number of bags are uploaded", func() {
			for i := 0; i < uploader.bagCount; i++ {
				uploadMan.AddBag(ctx, &bagMetadata{
					path:   fmt.Sprint("/tmp/uploadmanager_test/example/path/bag", i, ".db3"),
					number: i,
					isNew:  rnd.Int()%3 == 0,
				})
			}
			<-uploader.done
			So(len(uploader.bags), ShouldEqual, uploader.bagCount)
		})
	})
}
