//go:build darwin && cgo

package statusbar

/*
#cgo CFLAGS: -fblocks
#cgo LDFLAGS: -framework Cocoa

#include <stdlib.h>

void hoxi_companion_start(void);
void hoxi_companion_stop(void);
void hoxi_companion_reset_position(void);
void hoxi_companion_update(
	int visible,
	int minimized,
	int darkTheme,
	int showBubble,
	const char *persona,
	const char *personaName,
	const char *mood,
	const char *badgeLabel,
	const char *levelText,
	const char *dialogue,
	const char *targetSessionID,
	const char *accentHex,
	const void *avatarBytes,
	int avatarLen,
	int avatarChanged
);
*/
import "C"

import (
	"strings"
	"unsafe"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func PetSupported() bool { return true }

func StartPet() {
	C.hoxi_companion_start()
}

func StopPet() {
	C.hoxi_companion_stop()
}

func ResetPetPosition() {
	C.hoxi_companion_reset_position()
}

func SyncPet(p PetPayload) PetSyncResult {
	avatarBytes, avatarChanged, needAvatar := resolvePetAvatarCache(p.AvatarKey, p.AvatarDataURL)

	visible := C.int(0)
	if p.Visible {
		visible = 1
	}
	minimized := C.int(0)
	if p.Minimized {
		minimized = 1
	}
	darkTheme := C.int(0)
	if p.DarkTheme {
		darkTheme = 1
	}
	showBubble := C.int(0)
	if p.ShowBubble {
		showBubble = 1
	}
	changedFlag := C.int(0)
	if avatarChanged {
		changedFlag = 1
	}

	cPersona := C.CString(p.Persona)
	defer C.free(unsafe.Pointer(cPersona))
	cPersonaName := C.CString(p.PersonaName)
	defer C.free(unsafe.Pointer(cPersonaName))
	cMood := C.CString(p.Mood)
	defer C.free(unsafe.Pointer(cMood))
	cBadge := C.CString(p.BadgeLabel)
	defer C.free(unsafe.Pointer(cBadge))
	cLevel := C.CString(p.LevelText)
	defer C.free(unsafe.Pointer(cLevel))
	cDialogue := C.CString(p.Dialogue)
	defer C.free(unsafe.Pointer(cDialogue))
	cTargetSid := C.CString(p.TargetSessionID)
	defer C.free(unsafe.Pointer(cTargetSid))
	cAccent := C.CString(p.AccentHex)
	defer C.free(unsafe.Pointer(cAccent))

	var rawPtr unsafe.Pointer
	var rawLen C.int
	if len(avatarBytes) > 0 {
		rawPtr = C.CBytes(avatarBytes)
		rawLen = C.int(len(avatarBytes))
		defer C.free(rawPtr)
	}

	C.hoxi_companion_update(
		visible,
		minimized,
		darkTheme,
		showBubble,
		cPersona,
		cPersonaName,
		cMood,
		cBadge,
		cLevel,
		cDialogue,
		cTargetSid,
		cAccent,
		rawPtr,
		rawLen,
		changedFlag,
	)

	return PetSyncResult{
		Supported:  true,
		NeedAvatar: needAvatar,
	}
}

//export HoxiCompanionTriggerAction
func HoxiCompanionTriggerAction(kind *C.char, sessionID *C.char) {
	var k, sid string
	if kind != nil {
		k = strings.TrimSpace(C.GoString(kind))
	}
	if sessionID != nil {
		sid = strings.TrimSpace(C.GoString(sessionID))
	}
	switch k {
	case "tap":
		k = MenuActionCompanionTap
	case "toggle-minimize":
		k = MenuActionCompanionToggleMin
	case "dismiss":
		k = MenuActionCompanionDismiss
	}
	ctx := activeStatusBarCtx()
	fn := activeActionHandler()
	if ctx == nil && fn == nil {
		return
	}
	go func() {
		wakeWindow := k == MenuActionWorkspace ||
			k == MenuActionAttention ||
			k == MenuActionNewThread ||
			k == MenuActionSettings
		if wakeWindow && ctx != nil {
			wailsruntime.WindowShow(ctx)
			wailsruntime.WindowUnminimise(ctx)
		}
		invokeActionHandler(fn, k, sid)
	}()
}
