//go:build !darwin || !cgo

package notify

func platformStart() {}

func platformShow(Request) error {
	return ErrUnavailable
}
