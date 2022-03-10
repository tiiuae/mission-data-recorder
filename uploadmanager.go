package main

import (
	"container/heap"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

var globRegex = func() *regexp.Regexp {
	var b strings.Builder
	b.WriteString(`^/.+\.db3(`)
	for i, ext := range validBagExtensions {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(regexp.QuoteMeta(ext))
	}
	b.WriteString(`)?$`)
	return regexp.MustCompile(b.String())
}()

type uploaderInterface interface {
	UploadBag(context.Context, *bagMetadata) error
	WithCompression(compressionMode) uploaderInterface
}

type uploadManager struct {
	mutex sync.Mutex
	// +checklocks:mutex
	workerCount *semaphore.Weighted
	// +checklocks:mutex
	maxWorkerCount int
	// +checklocks:mutex
	uploader uploaderInterface
	// +checklocks:mutex
	queue bagQueue

	logger logger
	wg     sync.WaitGroup

	diagnostics *diagnosticsMonitor
}

func newUploadManager(workerCount int, uploader uploaderInterface, logger logger, diagnostics *diagnosticsMonitor) *uploadManager {
	return &uploadManager{
		workerCount:    semaphore.NewWeighted(int64(workerCount)),
		maxWorkerCount: workerCount,
		uploader:       uploader,
		logger:         logger,
		diagnostics:    diagnostics,
	}
}

func (m *uploadManager) LoadExistingBags(dir string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			m.logger.Errorf(`error during loading existing bags: failed to access "%s": %v`, dir, err)
		} else if globRegex.MatchString(path[len(dir):]) {
			if bag := newBagMetadata(path, 0, false); bag != nil {
				m.queue = append(m.queue, bag) // +checklocksignore
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	heap.Init(&m.queue)
	return nil
}

func (m *uploadManager) SetConfig(workerCount int, mode compressionMode) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.workerCount = semaphore.NewWeighted(int64(workerCount))
	m.maxWorkerCount = workerCount
	m.uploader = m.uploader.WithCompression(mode)
}

func (m *uploadManager) StartWorker(ctx context.Context) {
	if ctx.Err() == nil {
		m.wg.Add(1)
		go m.uploadNextBag(ctx)
	}
}

func (m *uploadManager) uploadNextBag(ctx context.Context) {
	defer m.wg.Done()
	bag, uploader, release := func() (*bagMetadata, uploaderInterface, func(int64)) {
		m.mutex.Lock()
		defer m.mutex.Unlock()
		if !m.workerCount.TryAcquire(1) {
			return nil, nil, func(i int64) {}
		}
		return m.nextBag(), m.uploader, m.workerCount.Release
	}()
	defer release(1)
	if bag == nil {
		return
	}
	m.logger.Infof("bag '%s' is ready", bag.path)
	err := uploader.UploadBag(ctx, bag)
	if err == nil {
		m.logger.Infof("bag '%s' uploaded successfully", bag.path)
		m.diagnostics.ReportSuccess("bag uploader", "ok")
		m.removeBagFiles(bag)
	} else {
		m.logger.Errorf("failed to upload bag '%s': %v", bag.path, err)
		m.diagnostics.ReportError("bag uploader", "failing: ", err)
		if errors.Is(err, errEmptyBag) {
			m.removeBagFiles(bag)
		}
	}
}

func (m *uploadManager) StartAllWorkers(ctx context.Context) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	for i := 0; i < m.maxWorkerCount; i++ {
		m.StartWorker(ctx)
	}
}

func (m *uploadManager) Wait() {
	m.wg.Wait()
}

func (m *uploadManager) removeBagFiles(bag *bagMetadata) {
	matches, err := filepath.Glob(escapeMatchPattern(bag.path) + "*")
	if err != nil {
		m.logger.Errorf("failed to remove files for '%s': %v", bag.path, err)
		return
	}
	for _, match := range matches {
		if err = os.Remove(match); err != nil {
			m.logger.Errorf("failed to remove '%s': %v", match, err)
		}
	}
	bagDir := filepath.Dir(bag.path)
	metadataFile := filepath.Join(bagDir, "metadata.yaml")
	if err = os.Remove(metadataFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.logger.Errorf("failed to remove '%s': %v", metadataFile, err)
	}
	err = os.Remove(bagDir)
	if err != nil &&
		!errors.Is(err, syscall.ENOTEMPTY) &&
		!errors.Is(err, syscall.EEXIST) {
		m.logger.Errorf("failed to remove '%s': %v", bagDir, err)
	}
}

func (m *uploadManager) AddBag(ctx context.Context, bag *bagMetadata) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	heap.Push(&m.queue, bag)
	m.StartWorker(ctx)
}

// +checklocks:m.mutex
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
