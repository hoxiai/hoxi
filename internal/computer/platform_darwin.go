//go:build darwin && cgo

package computer

/*
#cgo CFLAGS: -fblocks -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework ApplicationServices -framework ScreenCaptureKit -framework CoreImage -framework CoreMedia -framework CoreVideo -framework ImageIO
#include <stdlib.h>
char* hoxi_computer_call(const char* operation, const char* payload);
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"unsafe"
)

type nativeBackend struct{}

func nativeCall(ctx context.Context, operation string, payload any, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	op := C.CString(operation)
	data := C.CString(string(raw))
	defer C.free(unsafe.Pointer(op))
	defer C.free(unsafe.Pointer(data))
	response := C.hoxi_computer_call(op, data)
	if response == nil {
		return errors.New("native_computer_unavailable")
	}
	defer C.free(unsafe.Pointer(response))
	bytes := []byte(C.GoString(response))
	var status struct {
		Error string `json:"error"`
	}
	if err = json.Unmarshal(bytes, &status); err != nil {
		return err
	}
	if status.Error != "" {
		return errors.New(status.Error)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if out != nil {
		return json.Unmarshal(bytes, out)
	}
	return nil
}
func (nativeBackend) Permissions(ctx context.Context, prompt string) (Permissions, error) {
	var p Permissions
	err := nativeCall(ctx, "permissions", map[string]string{"prompt": prompt}, &p)
	return p, err
}
func (nativeBackend) Apps(ctx context.Context) ([]App, error) {
	var result struct {
		Apps []App `json:"apps"`
	}
	err := nativeCall(ctx, "apps", nil, &result)
	return result.Apps, err
}
func (nativeBackend) Inspect(ctx context.Context, app App, window string, image bool) (Snapshot, error) {
	var snapshot Snapshot
	err := nativeCall(ctx, "inspect", map[string]any{"pid": app.PID, "windowID": window, "image": image}, &snapshot)
	return snapshot, err
}
func (nativeBackend) Input(ctx context.Context, app App, window Window, action Action) error {
	key, _ := ParseKey(action.Key)
	return nativeCall(ctx, "input", map[string]any{"pid": app.PID, "windowID": window.ID, "action": action, "key": key, "expectedBounds": window.Bounds}, nil)
}
