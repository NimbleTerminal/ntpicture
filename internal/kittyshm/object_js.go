//go:build js && wasm

package kittyshm

import (
	"fmt"
	"syscall/js"
)

func registry() js.Value {
	return js.Global().Get("ghosttyKittySharedMemory")
}

func validRegistry(r js.Value) bool {
	return r.Type() == js.TypeObject && !r.IsNull() &&
		r.Get("set").Type() == js.TypeFunction && r.Get("delete").Type() == js.TypeFunction
}

// Supported reports whether the terminal has installed its browser registry.
func Supported() bool { return validRegistry(registry()) }

// Create publishes a tightly packed buffer under a unique name. fill runs before
// publication, so a failed fill leaves no registry entry.
func Create(size int, fill func([]byte) error) (obj *Object, err error) {
	r := registry()
	if !validRegistry(r) {
		return nil, ErrUnsupported
	}
	item, err := reservePending(size, fill)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			item.release()
		}
	}()
	name := newName()
	// Treat JS allocation/registry errors as failures so the caller can fall back.
	defer func() {
		if p := recover(); p != nil {
			if e, ok := p.(js.Error); ok {
				obj, err = nil, fmt.Errorf("create shared memory: %w", e)
			} else {
				panic(p)
			}
		}
	}()
	data := make([]byte, size)
	if err := fill(data); err != nil {
		return nil, err
	}
	array := js.Global().Get("Uint8Array").New(size)
	js.CopyBytesToJS(array, data)
	r.Call("set", name, array)
	obj = &Object{Name: name, Size: size, unlink: func() (err error) {
		defer func() {
			if p := recover(); p != nil {
				if e, ok := p.(js.Error); ok {
					err = fmt.Errorf("unlink shared memory: %w", e)
				} else {
					panic(p)
				}
			}
		}()
		r.Call("delete", name)
		item.release()
		return nil
	}}
	if r.Get("has").Type() == js.TypeFunction {
		item.bind(obj, func() (consumed bool) {
			if obj.Done() {
				return true
			}
			defer func() {
				if recover() != nil {
					consumed = false
				}
			}()
			return !r.Call("has", name).Bool()
		})
	}
	success = true
	return obj, nil
}
