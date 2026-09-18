//go:build !windows

package diag

import "errors"

var errUnsupported = errors.New("диагностика пока есть только для Windows")

func Collect() (Snapshot, error) { return Snapshot{Timestamps: true, DoH: true}, errUnsupported }

func CollectUser() UserSnapshot { return UserSnapshot{} }

func EnableTimestamps() error { return errUnsupported }

func StartBFE() error { return errUnsupported }

func UnloadWinDivert() error { return errUnsupported }

func StopBypass(uint32) ([]string, error) { return nil, errUnsupported }
