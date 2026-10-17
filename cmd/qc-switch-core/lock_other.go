//go:build !windows

package main

func instanceLock(string) (func(), error) { return func() {}, nil }
