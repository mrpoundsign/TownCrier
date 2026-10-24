package app

import (
	"errors"
	"strings"
	"sync"
)

// DefaultInstanceMutexName is the system-wide mutex identifier used to prevent duplicate TownCrier instances.
const DefaultInstanceMutexName = "TownCrier_SingleInstance_Mutex"

// ErrInstanceAlreadyRunning is returned when another TownCrier process (or lock holder) is already active.
var ErrInstanceAlreadyRunning = errors.New("another instance of TownCrier is already running")

var (
	inProcessLocksMu sync.Mutex
	inProcessLocks   = make(map[string]struct{})
)

// AcquireInstanceLock acquires a named single-instance lock for the current process.
// If the lock is already held by another process (or already held in-process), it returns ErrInstanceAlreadyRunning.
// The caller must invoke the returned release function on shutdown.
func AcquireInstanceLock(name string) (func(), error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = DefaultInstanceMutexName
	}

	inProcessLocksMu.Lock()
	if _, held := inProcessLocks[name]; held {
		inProcessLocksMu.Unlock()
		return nil, ErrInstanceAlreadyRunning
	}
	inProcessLocks[name] = struct{}{}
	inProcessLocksMu.Unlock()

	platRelease, err := acquirePlatformInstanceLock(name)
	if err != nil {
		inProcessLocksMu.Lock()
		delete(inProcessLocks, name)
		inProcessLocksMu.Unlock()
		return nil, err
	}

	var once sync.Once
	release := func() {
		once.Do(func() {
			if platRelease != nil {
				platRelease()
			}
			inProcessLocksMu.Lock()
			delete(inProcessLocks, name)
			inProcessLocksMu.Unlock()
		})
	}
	return release, nil
}
