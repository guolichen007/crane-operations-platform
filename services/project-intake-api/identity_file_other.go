//go:build !linux

package main

// Production ownership semantics are Linux root:root only; other OSes fail closed.
func readIdentityFile(string) ([]byte, error) { return nil, errIdentityInput }
