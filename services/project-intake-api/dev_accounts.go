package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
)

// devAccounts is an in-memory stand-in for the dedicated realm's account service,
// used only by APP_ENV=development with DEV_AUTH (synthetic browser tests). It
// keeps the same contract as keycloakAdmin: unique usernames, disabled until
// enabled, temporary password, exact single Baobei role. It never stores the
// password value, only whether one was set.
type devAccounts struct {
	mu    sync.Mutex
	users map[string]*devAccount
	// failNext makes the next call to the named step fail without acting (test hook).
	failNext string
	// lostNext makes the next call to the named step act, then report a transport
	// error (a timeout after Keycloak succeeded; test hook).
	lostNext string
	// down makes every call fail until cleared (provider outage; test hook).
	down bool
	// calls counts provider calls per step (test evidence: nothing is replayed).
	calls map[string]int
}

type devAccount struct {
	user     kcUser
	role     string
	password bool
}

func newDevAccounts() *devAccounts { return &devAccounts{users: map[string]*devAccount{}} }

func (d *devAccounts) fail(step string) error {
	if d.calls == nil {
		d.calls = map[string]int{}
	}
	d.calls[step]++
	if d.down {
		return errAccountProvider
	}
	if d.failNext == step {
		d.failNext = ""
		return errAccountProvider
	}
	return nil
}

// lost: the step acted; report a transport error anyway.
func (d *devAccounts) lost(step string) error {
	if d.lostNext == step {
		d.lostNext = ""
		return errAccountProvider
	}
	return nil
}

func (d *devAccounts) findByUsername(_ context.Context, username string) (*kcUser, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.fail("find"); err != nil {
		return nil, err
	}
	for _, a := range d.users {
		if a.user.Username == username {
			u := a.user
			return &u, nil
		}
	}
	return nil, nil
}

func (d *devAccounts) getUser(_ context.Context, id string) (*kcUser, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.fail("get"); err != nil {
		return nil, err
	}
	if a, ok := d.users[id]; ok {
		u := a.user
		return &u, nil
	}
	return nil, nil
}

func (d *devAccounts) createDisabled(_ context.Context, username, staffID string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.fail("create"); err != nil {
		return "", err
	}
	for _, a := range d.users {
		if a.user.Username == username {
			return "", accountProblem{409}
		}
	}
	id := newUUID()
	d.users[id] = &devAccount{user: kcUser{ID: id, Username: username, Enabled: boolp(false), Attributes: map[string][]string{"baobei_staff_id": {staffID}}}}
	if err := d.lost("create"); err != nil {
		return "", err
	}
	return id, nil
}

func (d *devAccounts) update(_ context.Context, id string, u kcUser) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.fail("update"); err != nil {
		return err
	}
	a, ok := d.users[id]
	if !ok {
		return accountProblem{404}
	}
	if u.Username != "" && u.Username != a.user.Username {
		for oid, o := range d.users {
			if oid != id && o.user.Username == u.Username {
				return accountProblem{409}
			}
		}
		a.user.Username = u.Username
	}
	if u.Enabled != nil {
		a.user.Enabled = boolp(*u.Enabled)
	}
	return d.lost("update")
}

func (d *devAccounts) setTemporaryPassword(_ context.Context, id, _ string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.fail("password"); err != nil {
		return err
	}
	a, ok := d.users[id]
	if !ok {
		return accountProblem{404}
	}
	a.password = true
	a.user.RequiredActions = []string{"UPDATE_PASSWORD"}
	return d.lost("password")
}

func (d *devAccounts) setExactRole(_ context.Context, id, role string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.fail("role"); err != nil {
		return err
	}
	a, ok := d.users[id]
	if !ok {
		return accountProblem{404}
	}
	a.role = role
	return d.lost("role")
}

func (d *devAccounts) roles(_ context.Context, id string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.fail("roles"); err != nil {
		return nil, err
	}
	if a, ok := d.users[id]; ok && a.role != "" {
		return []string{a.role}, nil
	}
	return []string{}, nil
}

func (d *devAccounts) logout(context.Context, string) error { return nil }

func (d *devAccounts) delete(_ context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.fail("delete"); err != nil {
		return err
	}
	delete(d.users, id)
	return nil
}

// enabledCount: enabled logins for a username (test helper).
func (d *devAccounts) enabledFor(username string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, a := range d.users {
		if a.user.Username == username && a.user.Enabled != nil && *a.user.Enabled {
			n++
		}
	}
	return n
}

func (d *devAccounts) callCount(step string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls[step]
}

func (d *devAccounts) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.users)
}

// signIn models the dedicated realm's login for development: a READY account
// signs in with its 11-digit phone. Returns the Keycloak UUID.
func (d *devAccounts) signIn(username string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, a := range d.users {
		if a.user.Username == username && a.user.Enabled != nil && *a.user.Enabled && a.password {
			a.user.RequiredActions = nil
			return id, true
		}
	}
	return "", false
}

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("random unavailable")
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
