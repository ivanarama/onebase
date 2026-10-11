package storage

import "errors"

// ErrInfoRegOwnershipConflict identifies an existing register key owned by a
// different document. The wrapped message retains the key/owner and localization;
// driver and owner-read errors do not belong to this class.
var ErrInfoRegOwnershipConflict = errors.New("information register ownership conflict")

type infoRegOwnershipError struct{ message error }

func (e infoRegOwnershipError) Error() string        { return e.message.Error() }
func (e infoRegOwnershipError) Unwrap() error        { return e.message }
func (e infoRegOwnershipError) Is(target error) bool { return target == ErrInfoRegOwnershipConflict }
