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

func TestUploadManager(t *testing.T) {
	const (
		workerCount = 5
		bagCount    = 100
	)
	var (
		uploadedBags      bagQueue
		done              = make(chan struct{})
		uploadedBagsMutex sync.Mutex
		uploadBag         = func(ctx context.Context, bag *bagMetadata) error {
			time.Sleep(500 * time.Millisecond)
			uploadedBagsMutex.Lock()
			defer uploadedBagsMutex.Unlock()
			uploadedBags = append(uploadedBags, bag)
			if len(uploadedBags) == bagCount {
				close(done)
			}
			return nil
		}
		uploadMan = newUploadManager(workerCount, uploadBag)
		ctx       = context.Background()
		rnd       = rand.New(rand.NewSource(42))
	)
	Convey("Scenario: uploadManager works correctly", t, func() {
		Convey("The correct number of bags are uploaded", func() {
			for i := 0; i < bagCount; i++ {
				uploadMan.AddBag(ctx, &bagMetadata{
					path:   fmt.Sprint("/tmp/uploadmanager_test/example/path/bag", i, ".db3"),
					number: i,
					isNew:  rnd.Int()%3 == 0,
				})
			}
			<-done
			So(len(uploadedBags), ShouldEqual, bagCount)
		})
	})
}
