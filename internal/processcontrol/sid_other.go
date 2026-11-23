//go:build !windows

package processcontrol

func CurrentWindowsSID() string { return "" }
