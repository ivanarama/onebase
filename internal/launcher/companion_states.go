package launcher

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"

	"github.com/google/uuid"
)

type companionStatus string

const (
	companionUndeclared  companionStatus = "undeclared"
	companionUnavailable companionStatus = "unavailable"
	companionRunning     companionStatus = "running"
	companionFailed      companionStatus = "failed"
	companionExited      companionStatus = "exited"
)

// The isolated Enterprise window may be a different launcher process.
// Use the local registry's lock and atomic writer for the status observed
// by both processes. Status never supplies executable paths to Start.
type companionState struct {
	Key     string          `json:"key"`
	Status  companionStatus `json:"status"`
	Path    string          `json:"path,omitempty"`
	Attempt string          `json:"attempt,omitempty"`
}

type companionLaunch struct {
	state companionState
	wait  func() error
}

// Serialize the check and start as well as status reads and writes.
var companionLaunchMu sync.Mutex

func lockCompanions(store *Store) (func(), error) {
	companionLaunchMu.Lock()
	lock, err := acquireStoreFileLock(store.path + ".companions.lock")
	if err != nil {
		companionLaunchMu.Unlock()
		return nil, err
	}
	return func() {
		defer companionLaunchMu.Unlock()
		if err := lock.Unlock(); err != nil {
			respondLog().Warn("не удалось снять блокировку сопутствующих программ", "err", err)
		}
	}, nil
}

func (s *Store) companionStatePath(baseID string) string {
	id := sha256.Sum256([]byte(baseID))
	return filepath.Join(filepath.Dir(s.path), fmt.Sprintf("companions-%x.json", id))
}

func (s *Store) readCompanionStates(baseID string) (map[string]companionState, error) {
	states := make(map[string]companionState)
	data, err := os.ReadFile(s.companionStatePath(baseID))
	if errors.Is(err, os.ErrNotExist) {
		return states, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &states); err != nil {
		return nil, err
	}
	if states == nil {
		states = make(map[string]companionState)
	}
	return states, nil
}

func (s *Store) writeCompanionStates(baseID string, states map[string]companionState) {
	data, err := json.Marshal(states)
	if err == nil {
		err = writeStoreFileAtomic(s.companionStatePath(baseID), data)
	}
	if err != nil {
		respondLog().Warn("не удалось записать состояние сопутствующих программ", "err", err)
	}
}

// recordCompanionLaunches is called with lockCompanions held when a store is
// available. A waiter checks the attempt ID before replacing a recorded status
// so completion of an older process cannot overwrite a new start.
func recordCompanionLaunches(store *Store, baseID string, launches []companionLaunch) {
	if len(launches) == 0 {
		return
	}
	persist := store != nil && baseID != ""
	var states map[string]companionState
	if persist {
		var err error
		states, err = store.readCompanionStates(baseID)
		if err != nil {
			respondLog().Warn("не удалось прочитать состояние сопутствующих программ", "err", err)
			states = make(map[string]companionState)
		}
	}
	for _, launch := range launches {
		state := launch.state
		if launch.wait != nil {
			state.Attempt = uuid.NewString()
			go func() {
				err := launch.wait()
				if persist {
					store.completeCompanionLaunch(baseID, state, err)
				}
			}()
		}
		if !persist {
			continue
		}
		previous := states[state.Key]
		if state.Status == companionRunning && launch.wait == nil &&
			previous.Status == companionRunning && previous.Path == state.Path {
			continue
		}
		states[state.Key] = state
	}
	if persist {
		store.writeCompanionStates(baseID, states)
	}
}

func (s *Store) completeCompanionLaunch(baseID string, state companionState, waitErr error) {
	unlock, err := lockCompanions(s)
	if err != nil {
		respondLog().Warn("не удалось обновить завершение сопутствующей программы", "err", err)
		return
	}
	defer unlock()
	states, err := s.readCompanionStates(baseID)
	if err != nil || states[state.Key].Attempt != state.Attempt {
		return
	}
	state.Status = companionExited
	if waitErr != nil {
		state.Status = companionFailed
	}
	state.Path = ""
	state.Attempt = ""
	states[state.Key] = state
	s.writeCompanionStates(baseID, states)
}

func (s *Store) companionStates(baseID string) []companionState {
	if _, err := os.Stat(s.companionStatePath(baseID)); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	unlock, err := lockCompanions(s)
	if err != nil {
		respondLog().Warn("не удалось прочитать состояние сопутствующих программ", "err", err)
		return nil
	}
	states, err := s.readCompanionStates(baseID)
	unlock()
	if err != nil {
		respondLog().Warn("не удалось прочитать состояние сопутствующих программ", "err", err)
		return nil
	}
	out := make([]companionState, 0, len(states))
	for _, state := range states {
		if runtime.GOOS == "windows" && state.Status == companionRunning && !companionProcessRunning(state.Path) {
			// The window which watched the process may have been closed.
			// A process with an unknown exit status is shown as completed.
			state.Status = companionExited
		}
		out = append(out, state)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
