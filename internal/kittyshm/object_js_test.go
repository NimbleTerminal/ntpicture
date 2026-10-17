//go:build js && wasm

package kittyshm

import (
	"bytes"
	"errors"
	"regexp"
	"syscall/js"
	"testing"
)

func TestBrowserObject(t *testing.T) {
	old := js.Global().Get("ghosttyKittySharedMemory")
	defer js.Global().Set("ghosttyKittySharedMemory", old)
	js.Global().Set("ghosttyKittySharedMemory", js.Undefined())
	if Supported() {
		t.Fatal("absent registry supported")
	}
	if _, err := Create(4, func([]byte) error { t.Fatal("fill on unsupported transport"); return nil }); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	r := js.Global().Get("Map").New()
	js.Global().Set("ghosttyKittySharedMemory", r)
	if !Supported() {
		t.Fatal("Map not supported")
	}
	fail := errors.New("fill failed")
	if _, err := Create(4, func([]byte) error { return fail }); !errors.Is(err, fail) {
		t.Fatal(err)
	}
	for _, size := range []int{-1, 0, maxPendingBytes + 1} {
		if _, err := Create(size, func([]byte) error { t.Fatal("invalid size called fill"); return nil }); err == nil {
			t.Fatal("invalid size accepted")
		}
	}
	if _, err := Create(4, nil); err == nil {
		t.Fatal("nil fill accepted")
	}
	if r.Get("size").Int() != 0 || pendingBytes() != 0 {
		t.Fatal("failed fill published or leaked accounting")
	}
	want := []byte{1, 2, 3, 4}
	obj, err := Create(4, func(b []byte) error { copy(b, want); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^/ntc-[0-9a-f]{20}$`).MatchString(obj.Name) || obj.Size != 4 || pendingBytes() != 4 {
		t.Fatalf("invalid object: %+v pending=%d", obj, pendingBytes())
	}
	if _, err := Create(maxPendingBytes, func([]byte) error { t.Fatal("cap exceeded called fill"); return nil }); err == nil {
		t.Fatal("pending byte cap not enforced")
	}
	got := make([]byte, 4)
	js.CopyBytesToGo(got, r.Call("get", obj.Name))
	if !bytes.Equal(got, want) {
		t.Fatal(got)
	}
	// Unlink uses the original registry, even if the global is replaced.
	js.Global().Set("ghosttyKittySharedMemory", js.Global().Get("Map").New())
	if err := obj.Unlink(); err != nil {
		t.Fatal(err)
	}
	if err := obj.Unlink(); err != nil {
		t.Fatal(err)
	}
	if r.Get("size").Int() != 0 || !obj.Done() || pendingBytes() != 0 {
		t.Fatal("entry or accounting leaked")
	}

	// Terminal deleting the live entry from the registry releases its pending reservation.
	r2 := js.Global().Get("Map").New()
	js.Global().Set("ghosttyKittySharedMemory", r2)
	objA, err := Create(16, func([]byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = objA.Unlink() }()
	r2.Call("delete", objA.Name)
	if pendingBytes() != 0 || !objA.Done() {
		t.Fatalf("consumed browser object kept reservation: pending=%d done=%v", pendingBytes(), objA.Done())
	}
}
