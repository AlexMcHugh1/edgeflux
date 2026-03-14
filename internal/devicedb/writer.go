package devicedb

import (
	"sync"
	"time"

	"github.com/edgeflux/edgeflux/internal/store"
)

type WriterConfig struct {
	QueueSize      int
	FlushInterval  time.Duration
	ImmediateBatch int
}

type Writer struct {
	db      *DB
	config  WriterConfig
	onError func(error, int)

	in     chan store.DeviceState
	stopCh chan struct{}
	doneCh chan struct{}

	overflowMu sync.Mutex
	overflow   map[string]store.DeviceState
}

func NewWriter(db *DB, cfg WriterConfig, onError func(error, int)) *Writer {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 4096
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 120 * time.Millisecond
	}
	if cfg.ImmediateBatch <= 0 {
		cfg.ImmediateBatch = 256
	}

	w := &Writer{
		db:       db,
		config:   cfg,
		onError:  onError,
		in:       make(chan store.DeviceState, cfg.QueueSize),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		overflow: make(map[string]store.DeviceState),
	}
	go w.run()
	return w
}

func (w *Writer) Enqueue(st store.DeviceState) {
	select {
	case w.in <- st:
		return
	default:
		// Keep only the latest snapshot per device when the queue is saturated.
		w.overflowMu.Lock()
		w.overflow[st.DeviceID] = st
		w.overflowMu.Unlock()
	}
}

func (w *Writer) Close() {
	close(w.stopCh)
	<-w.doneCh
}

func (w *Writer) run() {
	defer close(w.doneCh)

	ticker := time.NewTicker(w.config.FlushInterval)
	defer ticker.Stop()

	pending := make(map[string]store.DeviceState)

	flush := func() {
		if len(pending) == 0 {
			return
		}
		batch := make([]store.DeviceState, 0, len(pending))
		for _, st := range pending {
			batch = append(batch, st)
		}
		if err := w.db.UpsertDevicesBatch(batch); err != nil {
			if w.onError != nil {
				w.onError(err, len(batch))
			}
			return
		}
		clear(pending)
	}

	drainOverflow := func() {
		w.overflowMu.Lock()
		for id, st := range w.overflow {
			pending[id] = st
		}
		clear(w.overflow)
		w.overflowMu.Unlock()
	}

	drainInput := func() {
		for {
			select {
			case st := <-w.in:
				pending[st.DeviceID] = st
			default:
				return
			}
		}
	}

	for {
		select {
		case st := <-w.in:
			pending[st.DeviceID] = st
			if len(pending) >= w.config.ImmediateBatch {
				drainOverflow()
				flush()
			}
		case <-ticker.C:
			drainOverflow()
			drainInput()
			flush()
		case <-w.stopCh:
			drainInput()
			drainOverflow()
			flush()
			return
		}
	}
}
