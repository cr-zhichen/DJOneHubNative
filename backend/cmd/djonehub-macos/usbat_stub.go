//go:build !darwin || !cgo

package main

import (
	"errors"
	"strings"
	"time"
)

type usbAT struct{}

func openDJIUSBAT() (*usbAT, error) {
	return nil, errors.New("USB AT requires macOS cgo build with libusb")
}

func (u *usbAT) Close() {}

func (u *usbAT) Description() string { return "USB AT (unavailable)" }

func (u *usbAT) Command(_ string, _ time.Duration) (string, error) {
	return "", errors.New("USB AT is unavailable in this build")
}

func (u *usbAT) CommandChecked(command string, timeout time.Duration, beforeWrite func() error) (string, error) {
	if beforeWrite != nil {
		if err := beforeWrite(); err != nil {
			return "", err
		}
	}
	return u.Command(command, timeout)
}

func (u *usbAT) CommandWithPrompt(_ string, _ []byte, _ time.Duration) (string, error) {
	return "", errors.New("USB AT is unavailable in this build")
}

func atResponseIsError(resp string) bool {
	normalized := strings.ToUpper(strings.ReplaceAll(resp, "\r\n", "\n"))
	return strings.Contains(normalized, "\nERROR\n") ||
		strings.HasSuffix(normalized, "\nERROR") ||
		strings.Contains(normalized, "+CME ERROR:") ||
		strings.Contains(normalized, "+CMS ERROR:")
}

func atProbeSucceeded(resp string) bool {
	normalized := strings.ReplaceAll(strings.TrimSpace(resp), "\r\n", "\n")
	return normalized == "OK" || strings.HasSuffix(normalized, "\nOK")
}
