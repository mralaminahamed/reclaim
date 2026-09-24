//go:build !linux

package apps

import "time"

// DefaultEnv returns an Env with no application sources. Launchers on macOS
// are bundles, not .desktop entries, and access times there are not a usage
// record this package has been checked against -- so it reports nothing
// rather than something unverified.
func DefaultEnv(home string) Env {
	return Env{Home: home, Now: time.Now()}
}
