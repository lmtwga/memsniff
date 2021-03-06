package analysis

import (
	"errors"
	"github.com/box/memsniff/hotlist"
	"github.com/box/memsniff/protocol/model"
)

// worker accumulates usage data for a set of cache keys.
type worker struct {
	// hotlist of the busiest cache keys tracked by this worker
	hl hotlist.HotList
	// channel for reports of cache key activity
	kisChan chan []keyInfo
	// channel for requests for the current contents of the hotlist
	topRequest chan int
	// channel for results of top() requests
	topReply chan []hotlist.Entry
	// channel for requests to reset the hotlist to an empty state
	resetRequest chan bool
}

// keyInfo is the hotlist key for a cache key and value.
// All components must be comparable for equality.
type keyInfo struct {
	name string
	size int
}

// Weight implement hotlist.Item and gives each key weight equal to the size of
// the cache value.
func (ki keyInfo) Weight() int {
	return ki.size
}

// errQueueFull is returned by handleGetResponse if the worker cannot keep
// up with incoming calls.
var errQueueFull = errors.New("analysis worker queue full")

func newWorker() worker {
	w := worker{
		hl:           hotlist.NewPerfect(),
		kisChan:      make(chan []keyInfo, 1024),
		topRequest:   make(chan int),
		topReply:     make(chan []hotlist.Entry),
		resetRequest: make(chan bool),
	}
	go w.loop()
	return w
}

// handleEvents asynchronously processes events.
// handleEvents is threadsafe.
// When handleEvents returns, all relevant data from rs has been copied
// and is safe for the caller to discard.
func (w *worker) handleEvents(evts []model.Event) error {
	// Make sure we copy r.Key before we return, since it may be a pointer
	// into a buffer that will be overwritten.
	kis := make([]keyInfo, 0, len(evts))
	for i, evt := range evts {
		if evt.Type == model.EventGetHit {
			kis = kis[:i+1]
			kis[i] = keyInfo{evt.Key, evt.Size}
		}
	}
	select {
	case w.kisChan <- kis:
		return nil
	default:
		return errQueueFull
	}
}

// top returns the current contents of the hotlist for this worker.
// top is threadsafe.
func (w *worker) top(k int) []hotlist.Entry {
	w.topRequest <- k
	return <-w.topReply
}

// reset clear the contents of the hotlist for this worker.
// Some data may be lost if there is no external coordination of calls
// to top and handleGetResponse.
func (w *worker) reset() {
	w.resetRequest <- true
}

// close exits this worker. Calls to handleGetResponse after calling close
// will panic.
func (w *worker) close() {
	close(w.kisChan)
}

func (w *worker) loop() {
	for {
		select {
		case kis, ok := <-w.kisChan:
			if !ok {
				return
			}
			for _, ki := range kis {
				w.hl.AddWeighted(ki)
			}

		case k := <-w.topRequest:
			w.topReply <- w.hl.Top(k)

		case <-w.resetRequest:
			w.hl.Reset()
		}
	}
}
