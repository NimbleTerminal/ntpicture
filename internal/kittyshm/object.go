// Package kittyshm provides named pixel buffers for Kitty shared-memory frames.
package kittyshm

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// ErrUnsupported indicates that this process has no shared-memory transport.
var ErrUnsupported = errors.New("kitty shared memory is unsupported")

// Bound unconsumed objects across models. An unavailable/full shared-memory
// store returns an error so picture can fall back to direct transmission.
const maxPendingBytes = 64 << 20

type pendingItem struct {
	size     int
	released atomic.Bool
	consumed atomic.Pointer[func() bool]
	obj      atomic.Pointer[Object]
}

var pending struct {
	sync.Mutex
	bytes int
	items []*pendingItem
}

func reapConsumedPending() {
	pending.Lock()
	if len(pending.items) == 0 {
		pending.Unlock()
		return
	}
	snapshot := append([]*pendingItem(nil), pending.items...)
	pending.Unlock()

	for _, item := range snapshot {
		if item.released.Load() {
			continue
		}
		if fn := item.consumed.Load(); fn != nil && (*fn)() {
			item.release()
			if o := item.obj.Load(); o != nil {
				o.done.Store(true)
			}
		}
	}
}

func (item *pendingItem) release() {
	if item == nil || !item.released.CompareAndSwap(false, true) {
		return
	}
	pending.Lock()
	pending.bytes -= item.size
	for i, it := range pending.items {
		if it == item {
			last := len(pending.items) - 1
			pending.items[i] = pending.items[last]
			pending.items[last] = nil
			pending.items = pending.items[:last]
			break
		}
	}
	pending.Unlock()
}

func (item *pendingItem) bind(obj *Object, consumed func() bool) {
	if item == nil {
		return
	}
	item.obj.Store(obj)
	if consumed != nil {
		item.consumed.Store(&consumed)
	}
}

func pendingBytes() int {
	reapConsumedPending()
	pending.Lock()
	defer pending.Unlock()
	return pending.bytes
}

func reservePending(size int, fill func([]byte) error) (*pendingItem, error) {
	if size <= 0 || size > maxPendingBytes || fill == nil {
		return nil, fmt.Errorf("invalid shared-memory buffer size or fill")
	}
	reapConsumedPending()
	pending.Lock()
	if size > maxPendingBytes-pending.bytes {
		pending.Unlock()
		return nil, fmt.Errorf("shared-memory pending-byte limit reached")
	}
	item := &pendingItem{size: size}
	pending.bytes += size
	pending.items = append(pending.items, item)
	pending.Unlock()
	return item, nil
}

// Object is a named buffer. The terminal consumes it when it reads the APC;
// producers must Unlink objects for frames that are dropped before transmission.
type Object struct {
	Name   string
	Size   int
	unlink func() error
	retire func()
	done   atomic.Bool
}

// Unlink removes the object. Calling it after consumption or more than once is safe.
func (o *Object) Unlink() error {
	if o == nil || o.unlink == nil {
		return nil
	}
	err := o.unlink()
	if err == nil {
		o.done.Store(true)
	}
	return err
}

// Retire releases a submitted frame once the terminal has consumed it. Browser
// entries can be removed immediately; native readers get time to drain the tty.
func (o *Object) Retire() {
	if o == nil || o.Done() {
		return
	}
	if o.retire != nil {
		o.retire()
	} else {
		_ = o.Unlink()
	}
}

// Done reports whether producer-side cleanup has finished.
func (o *Object) Done() bool { return o == nil || o.done.Load() }

func newName() string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	return "/ntc-" + hex.EncodeToString(b[:])
}
