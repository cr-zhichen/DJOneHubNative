//go:build !darwin || !cgo

package main

import "net/http"

func enableModuleNetworkWake() error  { return nil }
func disableModuleNetworkWake() error { return nil }

func (a *app) enableNetworkWakeAPI(http.ResponseWriter, *http.Request) {}
func (a *app) disableNetworkWakeAPI(http.ResponseWriter, *http.Request) {}
func (a *app) networkWakeStatusAPI(http.ResponseWriter, *http.Request) {}
