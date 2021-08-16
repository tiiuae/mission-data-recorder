package main

import (
	"container/heap"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"golang.org/x/sync/semaphore"
)

type bagQueue []*bagMetadata

func (a bagQueue) Len() int { return len(a) }

func (a bagQueue) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
	a[i].index = i
	a[j].index = j
}

func (a bagQueue) Less(i, j int) bool {
	if a[i].isNew && a[j].isNew {
		return a[i].number > a[j].number
	} else if !a[i].isNew && !a[j].isNew {
		return a[i].number < a[j].number
	} else {
		return a[i].isNew
	}
}

func (a *bagQueue) Push(x interface{}) {
	n := len(*a)
	item := x.(*bagMetadata)
	item.index = n
	*a = append(*a, item)
}

func (a *bagQueue) Pop() interface{} {
	old := *a
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*a = old[0 : n-1]
	return item
}

type uploadBagFunc = func(context.Context, *bagMetadata) error

type uploadManager struct {
	workerCount *semaphore.Weighted
	uploadBag   uploadBagFunc
	queue       bagQueue
	mutex       sync.Mutex
}

func newUploadManager(workerCount int, uploadBag uploadBagFunc) *uploadManager {
	sem := semaphore.NewWeighted(int64(workerCount))
	return &uploadManager{
		workerCount: unsafe.Pointer(sem),
		uploadBag:   uploadBag,
	}
}

func (m *uploadManager) LoadExistingBags(dir string) error {
	dir = escapeMatchPattern(filepath.Clean(dir))
	if err := m.addGlob(dir + "/*.db3"); err != nil {
		return err
	}
	if err := m.addGlob(dir + "/*/*.db3"); err != nil {
		return err
	}
	heap.Init(&m.queue)
	return nil
}

func (m *uploadManager) addGlob(pattern string) error {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	for _, match := range matches {
		if bag := newBagMetadata(match, 0, false); bag != nil {
			m.queue = append(m.queue, bag)
		}
	}
	return nil
}

func (m *uploadManager) SetWorkerCount(n int) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.workerCount = semaphore.NewWeighted(int64(n))
}

func (m *uploadManager) StartWorker(ctx context.Context) {
	for ctx.Err() == nil {
		bag := func() *bagMetadata {
			m.mutex.Lock()
			defer m.mutex.Unlock()
			if m.workerCount.TryAcquire(1) {
				return m.nextBag()
			}
			return nil
		}()
		if bag == nil {
			return
		}
		defer m.workerCount.Release(1)
		log.Printf("bag '%s' is ready", bag.path)
		err := m.uploadBag(ctx, bag)
		if err == nil {
			log.Printf("bag '%s' uploaded successfully", bag.path)
			m.removeBagFiles(bag)
		} else {
			log.Printf("failed to upload bag '%s': %v", bag.path, err)
			if errors.Is(err, errEmptyBag) {
				m.removeBagFiles(bag)
			}
		}
	}
}

func (m *uploadManager) removeBagFiles(bag *bagMetadata) {
	matches, err := filepath.Glob(escapeMatchPattern(bag.path) + "*")
	if err != nil {
		log.Printf("failed to remove files for '%s': %v", bag.path, err)
		return
	}
	for _, match := range matches {
		if err = os.Remove(match); err != nil {
			log.Printf("failed to remove '%s': %v", match, err)
		}
	}
	bagDir := filepath.Dir(bag.path)
	err = os.Remove(bagDir)
	if err != nil &&
		!errors.Is(err, syscall.ENOTEMPTY) &&
		!errors.Is(err, syscall.EEXIST) {
		log.Printf("failed to remove '%s': %v", bagDir, err)
	}
}

func (m *uploadManager) AddBag(ctx context.Context, bag *bagMetadata) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	heap.Push(&m.queue, bag)
	go m.StartWorker(ctx)
}

func (m *uploadManager) nextBag() *bagMetadata {
	if len(m.queue) == 0 {
		return nil
	}
	bag := heap.Pop(&m.queue).(*bagMetadata)
	if len(m.queue) < cap(m.queue)/3 {
		old := m.queue
		m.queue = make(bagQueue, len(old))
		copy(m.queue, old)
	}
	return bag
}
