//go:build (!darwin || !cgo) && !windows

package notify

func platformStart() {}

func platformStatus() PermissionStatus {
	return PermissionUnavailable
}

func platformRequestPermission() PermissionStatus {
	return PermissionUnavailable
}

func platformShow(Request) error {
	return ErrUnavailable
}
