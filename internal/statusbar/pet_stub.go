//go:build !darwin || !cgo

package statusbar

func PetSupported() bool { return false }

func StartPet() {}

func StopPet() {}

func SyncPet(_ PetPayload) PetSyncResult {
	return PetSyncResult{Supported: false, NeedAvatar: false}
}

func ResetPetPosition() {}
