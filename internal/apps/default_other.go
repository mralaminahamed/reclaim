//go:build !linux && !darwin

package apps

import "time"

// DefaultEnv returns an Env with no application sources: this platform has
// no launcher format this package knows how to read.
func DefaultEnv(home string) Env {
	return Env{Home: home, Now: time.Now()}
}

// FindIdle reports no applications: there is no launcher or bundle format
// here to look for evidence in.
func FindIdle(home string, idle time.Duration) Result {
	return Result{}
}
