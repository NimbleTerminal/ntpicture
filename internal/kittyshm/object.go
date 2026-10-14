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

var pending struct {
	sync.Mutex
	bytes int
}

func pendingBytes() int {
	pending.Lock()
	defer pending.Unlock()
	return pending.bytes
}

func reservePending(size int, fill func([]byte) error) (func(), error) {
	if size <= 0 || size > maxPendingBytes || fill == nil {
		return nil, fmt.Errorf("invalid shared-memory buffer size or fill")
	}
	pending.Lock()
	if size > maxPendingBytes-pending.bytes {
		pending.Unlock()
		return nil, fmt.Errorf("shared-memory pending-byte limit reached")
	}
	pending.bytes += size
	pending.Unlock()
	var releaseOnce sync.Once
	return func() {
		releaseOnce.Do(func() {
			pending.Lock()
			pending.bytes -= size
			pending.Unlock()
		})
	}, nil
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
