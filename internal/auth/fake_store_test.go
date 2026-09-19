package auth

import (
	"context"
	"sort"
	"sync"
	"time"

	"emsim/internal/platform/audit"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// fakeTx satisfies pgx.Tx by embedding a nil pgx.Tx: every promoted method
// panics if called, which is fine — fakeStore's own methods never touch
// it, they only use it as an opaque token threaded through WithTx, the
// same way a real pgx.Tx is threaded through Store's other methods.
type fakeTx struct{ pgx.Tx }

// fakeStore is an in-memory Store for Service's unit tests. WithTx
// approximates pgx.Tx semantics well enough for these tests: it snapshots
// state before calling fn and restores the snapshot if fn returns an
// error, so a test can exercise "nothing was written" on a failure path
// without a real database.
type fakeStore struct {
	mu sync.Mutex

	usersByLogin map[string]User
	usersByID    map[uuid.UUID]User

	workstationsByNumber map[int]Workstation
	workstationsByID     map[uuid.UUID]Workstation

	sessions map[string]Session // keyed by string(session.ID)

	auditEntries []audit.Entry

	// Hooks a test can set to force a specific method to fail, simulating
	// a storage error unrelated to the input's own shape.
	failInsertSession bool
	failInsertUser    bool
	failDeleteSession bool
	failAuditRecord   bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		usersByLogin:         map[string]User{},
		usersByID:            map[uuid.UUID]User{},
		workstationsByNumber: map[int]Workstation{},
		workstationsByID:     map[uuid.UUID]Workstation{},
		sessions:             map[string]Session{},
	}
}

func (f *fakeStore) addUser(u User) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.usersByLogin[u.Login] = u
	f.usersByID[u.ID] = u
}

func (f *fakeStore) addWorkstation(w Workstation) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workstationsByNumber[w.Number] = w
	f.workstationsByID[w.ID] = w
}

type fakeSnapshot struct {
	usersByLogin         map[string]User
	usersByID            map[uuid.UUID]User
	workstationsByNumber map[int]Workstation
	workstationsByID     map[uuid.UUID]Workstation
	sessions             map[string]Session
	auditEntries         []audit.Entry
}

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (f *fakeStore) snapshotLocked() fakeSnapshot {
	return fakeSnapshot{
		usersByLogin:         cloneMap(f.usersByLogin),
		usersByID:            cloneMap(f.usersByID),
		workstationsByNumber: cloneMap(f.workstationsByNumber),
		workstationsByID:     cloneMap(f.workstationsByID),
		sessions:             cloneMap(f.sessions),
		auditEntries:         append([]audit.Entry{}, f.auditEntries...),
	}
}

func (f *fakeStore) restoreLocked(snap fakeSnapshot) {
	f.usersByLogin = snap.usersByLogin
	f.usersByID = snap.usersByID
	f.workstationsByNumber = snap.workstationsByNumber
	f.workstationsByID = snap.workstationsByID
	f.sessions = snap.sessions
	f.auditEntries = snap.auditEntries
}

func (f *fakeStore) WithTx(_ context.Context, fn func(tx pgx.Tx) error) error {
	f.mu.Lock()
	snap := f.snapshotLocked()
	f.mu.Unlock()

	if err := fn(fakeTx{}); err != nil {
		f.mu.Lock()
		f.restoreLocked(snap)
		f.mu.Unlock()
		return err
	}
	return nil
}

func (f *fakeStore) UserByLogin(_ context.Context, _ pgx.Tx, login string) (User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.usersByLogin[login]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (f *fakeStore) UserByID(_ context.Context, _ pgx.Tx, id uuid.UUID) (User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.usersByID[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (f *fakeStore) ListUsers(_ context.Context, _ pgx.Tx, page, pageSize int) ([]User, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	all := make([]User, 0, len(f.usersByID))
	for _, u := range f.usersByID {
		all = append(all, u)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Login < all[j].Login })

	total := len(all)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := min(start+pageSize, total)
	return append([]User{}, all[start:end]...), total, nil
}

func (f *fakeStore) InsertUser(_ context.Context, _ pgx.Tx, u User) (User, error) {
	if f.failInsertUser {
		return User{}, ErrStorage
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.usersByLogin[u.Login]; exists {
		return User{}, ErrLoginTaken
	}
	u.CreatedAt = time.Now()
	f.usersByLogin[u.Login] = u
	f.usersByID[u.ID] = u
	return u, nil
}

func (f *fakeStore) UpdateUser(_ context.Context, _ pgx.Tx, id uuid.UUID, update UserUpdate) (User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.usersByID[id]
	if !ok {
		return User{}, ErrNotFound
	}
	if update.PasswordHashSet {
		u.PasswordHash = *update.PasswordHash
	}
	if update.FullName != nil {
		u.FullName = *update.FullName
	}
	if update.Role != nil {
		u.Role = *update.Role
	}
	if update.ServiceCodeSet {
		u.ServiceCode = update.ServiceCode
	}
	if update.Level != nil {
		u.Level = *update.Level
	}
	if update.Active != nil {
		u.Active = *update.Active
	}
	f.usersByID[id] = u
	f.usersByLogin[u.Login] = u
	return u, nil
}

func (f *fakeStore) CountActiveAdmins(_ context.Context, _ pgx.Tx) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, u := range f.usersByID {
		if u.Role == RoleAdmin && u.Active {
			count++
		}
	}
	return count, nil
}

func (f *fakeStore) WorkstationByNumber(_ context.Context, _ pgx.Tx, number int) (Workstation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, ok := f.workstationsByNumber[number]
	if !ok {
		return Workstation{}, ErrNotFound
	}
	return w, nil
}

func (f *fakeStore) WorkstationByID(_ context.Context, _ pgx.Tx, id uuid.UUID) (Workstation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, ok := f.workstationsByID[id]
	if !ok {
		return Workstation{}, ErrNotFound
	}
	return w, nil
}

func (f *fakeStore) ListWorkstations(_ context.Context, _ pgx.Tx) ([]Workstation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	all := make([]Workstation, 0, len(f.workstationsByID))
	for _, w := range f.workstationsByID {
		all = append(all, w)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Number < all[j].Number })
	return all, nil
}

// UpsertWorkstations mirrors internal/auth/postgres.Store.UpsertWorkstations:
// matched by Number, an existing row keeps its id and is reactivated, a
// new one is assigned a fresh one.
func (f *fakeStore) UpsertWorkstations(_ context.Context, _ pgx.Tx, workstations []Workstation) ([]Workstation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]Workstation, 0, len(workstations))
	for _, w := range workstations {
		if existing, ok := f.workstationsByNumber[w.Number]; ok {
			existing.Label, existing.IPAddress, existing.Active = w.Label, w.IPAddress, true
			f.workstationsByNumber[w.Number] = existing
			f.workstationsByID[existing.ID] = existing
			result = append(result, existing)
			continue
		}
		w.ID = uuid.New()
		w.Active = true
		f.workstationsByNumber[w.Number] = w
		f.workstationsByID[w.ID] = w
		result = append(result, w)
	}
	return result, nil
}

func (f *fakeStore) DeactivateWorkstationsNotIn(_ context.Context, _ pgx.Tx, keepNumbers []int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	keep := make(map[int]struct{}, len(keepNumbers))
	for _, n := range keepNumbers {
		keep[n] = struct{}{}
	}
	for number, w := range f.workstationsByNumber {
		if _, ok := keep[number]; !ok && w.Active {
			w.Active = false
			f.workstationsByNumber[number] = w
			f.workstationsByID[w.ID] = w
		}
	}
	return nil
}

func (f *fakeStore) InsertSession(_ context.Context, _ pgx.Tx, session Session, ttl time.Duration) (Session, error) {
	if f.failInsertSession {
		return Session{}, ErrStorage
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	session.CreatedAt = now
	session.LastSeenAt = now
	session.ExpiresAt = now.Add(ttl)
	f.sessions[string(session.ID)] = session
	return session, nil
}

func (f *fakeStore) SessionByID(_ context.Context, _ pgx.Tx, id []byte) (SessionLookup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessions[string(id)]
	if !ok || !session.ExpiresAt.After(time.Now()) {
		return SessionLookup{}, ErrNotFound
	}
	user, ok := f.usersByID[session.UserID]
	if !ok {
		return SessionLookup{}, ErrNotFound
	}
	lookup := SessionLookup{Session: session, User: user}
	if session.WorkstationID != nil {
		if w, ok := f.workstationsByID[*session.WorkstationID]; ok {
			lookup.Workstation = &w
		}
	}
	return lookup, nil
}

func (f *fakeStore) TouchSession(_ context.Context, _ pgx.Tx, id []byte, staleAfter, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessions[string(id)]
	if !ok {
		return nil
	}
	if time.Since(session.LastSeenAt) < staleAfter {
		return nil
	}
	now := time.Now()
	session.LastSeenAt = now
	session.ExpiresAt = now.Add(ttl)
	f.sessions[string(id)] = session
	return nil
}

func (f *fakeStore) DeleteSession(_ context.Context, _ pgx.Tx, id []byte) error {
	if f.failDeleteSession {
		return ErrStorage
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, string(id))
	return nil
}

func (f *fakeStore) DeleteUserSessions(_ context.Context, _ pgx.Tx, userID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, session := range f.sessions {
		if session.UserID == userID {
			delete(f.sessions, id)
		}
	}
	return nil
}

func (f *fakeStore) AuditRecord(_ context.Context, _ pgx.Tx, entry audit.Entry) error {
	if f.failAuditRecord {
		return ErrStorage
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auditEntries = append(f.auditEntries, entry)
	return nil
}

func (f *fakeStore) auditEntriesByAction(action string) []audit.Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []audit.Entry
	for _, e := range f.auditEntries {
		if e.Action == action {
			out = append(out, e)
		}
	}
	return out
}

var _ Store = (*fakeStore)(nil)
