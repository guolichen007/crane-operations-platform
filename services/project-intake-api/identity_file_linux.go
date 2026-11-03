//go:build linux

package main

import (
	"io"
	"os"
	"syscall"
)

func readIdentityFile(path string) ([]byte, error) {
	if os.Geteuid() != 0 {
		return nil, errIdentityInput
	}
	fd, e := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, errIdentityInput
	}
	f := os.NewFile(uintptr(fd), "identity-input")
	defer f.Close()
	s, e := f.Stat()
	if e != nil || !s.Mode().IsRegular() || s.Size() > identityLimit {
		return nil, errIdentityInput
	}
	st, ok := s.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || st.Gid != 0 || (s.Mode().Perm() != 0600 && s.Mode().Perm() != 0400) {
		return nil, errIdentityInput
	}
	b, e := io.ReadAll(io.LimitReader(f, identityLimit+1))
	if e != nil || len(b) > identityLimit {
		return nil, errIdentityInput
	}
	return b, nil
}
