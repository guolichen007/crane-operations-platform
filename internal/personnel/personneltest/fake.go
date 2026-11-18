// Package personneltest provides a faithful in-memory Keycloak user store for tests of the
// personnel service and its HTTP surface (exact usernames, managed attribute, disabled-on-create,
// temporary password -> UPDATE_PASSWORD, logout, lost create responses, outages).
package personneltest

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/guolichen007/crane-operations-platform/internal/kcadmin"
)

// Fake mirrors the Keycloak semantics the service depends on (exact username uniqueness,
// managed attribute, disabled-on-create, temporary password → UPDATE_PASSWORD, logout).
type Fake struct {
	mu        sync.Mutex
	Users     map[string]*kcadmin.User
	Passwords map[string]string
	Sessions  map[string]int
	Down      bool
	n         int
	// LoseCreateResponse: the user is created but the caller sees an error (lost response).
	LoseCreateResponse bool
	// Hook (optional) is called, WITHOUT the fake's mutex, before every mutating call with the
	// operation name ("SetEnabled", "SetTemporaryPassword", "Logout", "UpdateProfile",
	// "CreateDisabled"). Concurrency tests block in it to hold an operation inside its Keycloak
	// phase while a competing operation is attempted.
	Hook func(op, id string)
	// Log records mutating calls in order ("SetEnabled:<id>:true", ...).
	Log []string
}

func (f *Fake) hook(op, id string) {
	if f.Hook != nil {
		f.Hook(op, id)
	}
}

func (f *Fake) record(entry string) {
	f.Log = append(f.Log, entry)
}

func NewFake() *Fake {
	return &Fake{Users: map[string]*kcadmin.User{}, Passwords: map[string]string{}, Sessions: map[string]int{}}
}

func (f *Fake) newID() string {
	f.n++
	return "00000000-0000-4000-8000-" + strings.Repeat("0", 12-len(strconv.Itoa(f.n))) + strconv.Itoa(f.n)
}

func (f *Fake) FindByUsername(_ context.Context, u string) (*kcadmin.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Down {
		return nil, kcadmin.ErrUnavailable
	}
	for _, v := range f.Users {
		if v.Username == u {
			c := *v
			return &c, nil
		}
	}
	return nil, nil
}
func (f *Fake) Get(_ context.Context, id string) (*kcadmin.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Down {
		return nil, kcadmin.ErrUnavailable
	}
	if v, ok := f.Users[id]; ok {
		c := *v
		return &c, nil
	}
	return nil, nil
}
func (f *Fake) CreateDisabled(_ context.Context, p kcadmin.Profile) (string, error) {
	f.hook("CreateDisabled", p.Username)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Down {
		return "", kcadmin.ErrUnavailable
	}
	for _, v := range f.Users {
		if v.Username == p.Username {
			return "", kcadmin.ErrConflict
		}
	}
	id := f.newID()
	no := false
	f.Users[id] = &kcadmin.User{ID: id, Username: p.Username, Enabled: &no, FirstName: p.DisplayName,
		Attributes: map[string][]string{kcadmin.ManagedAttribute: {"true"}, "crane_user_id": {p.LocalUserID}}}
	if f.LoseCreateResponse {
		f.LoseCreateResponse = false
		return "", kcadmin.ErrUnavailable
	}
	return id, nil
}
func (f *Fake) managed(id string) (*kcadmin.User, error) {
	if f.Down {
		return nil, kcadmin.ErrUnavailable
	}
	v, ok := f.Users[id]
	if !ok {
		return nil, kcadmin.ErrNotFound
	}
	if !v.Managed() {
		return nil, kcadmin.ErrNotManaged
	}
	return v, nil
}
func (f *Fake) UpdateProfile(_ context.Context, id string, p kcadmin.Profile) error {
	f.hook("UpdateProfile", id)
	f.mu.Lock()
	defer f.mu.Unlock()
	v, err := f.managed(id)
	if err == nil {
		v.FirstName = p.DisplayName
	}
	return err
}
func (f *Fake) SetEnabled(_ context.Context, id string, enabled bool) error {
	f.hook("SetEnabled", id)
	f.mu.Lock()
	defer f.mu.Unlock()
	v, err := f.managed(id)
	if err == nil {
		v.Enabled = &enabled
		f.record("SetEnabled:" + id + ":" + strconv.FormatBool(enabled))
	}
	return err
}
func (f *Fake) SetTemporaryPassword(_ context.Context, id string, pw []byte) error {
	f.hook("SetTemporaryPassword", id)
	f.mu.Lock()
	defer f.mu.Unlock()
	v, err := f.managed(id)
	if err == nil {
		f.record("SetTemporaryPassword:" + id)
		f.Passwords[id] = string(pw)
		v.RequiredActions = []string{"UPDATE_PASSWORD"}
	}
	return err
}
func (f *Fake) Logout(_ context.Context, id string) error {
	f.hook("Logout", id)
	f.mu.Lock()
	defer f.mu.Unlock()
	_, err := f.managed(id)
	if err == nil {
		f.Sessions[id] = 0
	}
	return err
}
func (f *Fake) DeleteUnbound(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Down {
		return kcadmin.ErrUnavailable
	}
	v, ok := f.Users[id]
	if !ok {
		return nil
	}
	if !v.Managed() || (v.Enabled != nil && *v.Enabled) {
		return kcadmin.ErrNotManaged
	}
	delete(f.Users, id)
	return nil
}
func (f *Fake) Adopt(_ context.Context, id, local string) (*kcadmin.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.Users[id]
	if !ok {
		return nil, kcadmin.ErrNotFound
	}
	v.Attributes = map[string][]string{kcadmin.ManagedAttribute: {"true"}, "crane_user_id": {local}}
	c := *v
	return &c, nil
}
func (f *Fake) AddExternal(username string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.newID()
	yes := true
	f.Users[id] = &kcadmin.User{ID: id, Username: username, Enabled: &yes}
	return id
}

// Password returns the current password of a user (tests only).
func (f *Fake) Password(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Passwords[id]
}

// Enabled reports the Keycloak enabled flag of a user (tests only).
func (f *Fake) Enabled(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.Users[id]
	return ok && u.Enabled != nil && *u.Enabled
}
