//go:build !openbsd
// +build !openbsd

package privsep

func PledgePromises(promises string) error {
	return nil
}

func Unveil(path string, flags string) error {
	return nil
}

func UnveilBlock() error {
	return nil
}
