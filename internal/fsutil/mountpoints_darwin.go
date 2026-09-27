package fsutil

// MountPoints returns nothing on macOS. There is no bind mount to hide behind
// a shared device number, so the device comparison the walkers already make
// is the whole test.
func MountPoints() []string { return nil }
