package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Filter narrows a read of audit_log. Zero fields do not filter.
// ActionPrefix matches "admin." or "admin.user.create" alike: it is a
// prefix over the dot-namespaced action.
type Filter struct {
	From         *time.Time
	To           *time.Time
	ActorID      *uuid.UUID
	ActionPrefix string
	Outcome      Outcome
	ResourceType string
}

// Row is one audit_log row as an administrator reads it. Details holds
// only what the writing module chose to record — by Record's own rule,
// never passwords, trainee texts or full names.
type Row struct {
	ID           int64
	At           time.Time
	ActorID      *uuid.UUID
	ActorRole    string
	Action       string
	ResourceType string
	ResourceID   *uuid.UUID
	Outcome      Outcome
	RequestID    string
	Details      map[string]any
}

// Querier is the part of a pool or transaction List needs.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// MaxPageSize bounds one List call.
const MaxPageSize = 500

var actionPrefixPattern = regexp.MustCompile(`^[a-z_.]{0,64}$`)

// Validate reports ErrInvalidEntry for a filter that cannot be a real
// value (it never reaches SQL as anything but a bound parameter, but a
// malformed prefix is a client error worth naming).
func (f Filter) Validate() error {
	if !actionPrefixPattern.MatchString(f.ActionPrefix) {
		return ErrInvalidEntry
	}
	if f.Outcome != "" && !f.Outcome.valid() {
		return ErrInvalidEntry
	}
	if f.ResourceType != "" && !resourceTypePattern.MatchString(f.ResourceType) {
		return ErrInvalidEntry
	}
	if f.From != nil && f.To != nil && f.To.Before(*f.From) {
		return ErrInvalidEntry
	}
	return nil
}

// List returns up to limit rows, newest first, strictly older than
// beforeID (0 = from the newest) — keyset pagination on the identity
// column, stable while new rows keep arriving.
func List(ctx context.Context, db Querier, f Filter, beforeID int64, limit int) ([]Row, error) {
	if db == nil || f.Validate() != nil || beforeID < 0 {
		return nil, ErrInvalidEntry
	}
	if limit < 1 || limit > MaxPageSize {
		limit = MaxPageSize
	}
	where, args := f.clauses(beforeID)
	query := `SELECT id, at, actor_id, actor_role, action, resource_type, resource_id, outcome, request_id, details FROM audit_log`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", len(args))

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	out := make([]Row, 0, limit)
	for rows.Next() {
		var (
			r         Row
			actorRole *string
			requestID *string
			outcome   string
			details   []byte
		)
		if err := rows.Scan(&r.ID, &r.At, &r.ActorID, &actorRole, &r.Action, &r.ResourceType, &r.ResourceID, &outcome, &requestID, &details); err != nil {
			return nil, ErrStorage
		}
		r.Outcome = Outcome(outcome)
		if actorRole != nil {
			r.ActorRole = *actorRole
		}
		if requestID != nil {
			r.RequestID = *requestID
		}
		r.Details = map[string]any{}
		if len(details) > 0 {
			if err := json.Unmarshal(details, &r.Details); err != nil {
				return nil, ErrStorage
			}
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrStorage
	}
	return out, nil
}

// clauses turns the filter (and the keyset bound) into WHERE conditions
// with their positional arguments.
func (f Filter) clauses(beforeID int64) (where []string, args []any) {
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if beforeID > 0 {
		add("id < $%d", beforeID)
	}
	if f.From != nil {
		add("at >= $%d", *f.From)
	}
	if f.To != nil {
		add("at < $%d", *f.To)
	}
	if f.ActorID != nil {
		add("actor_id = $%d", *f.ActorID)
	}
	if f.ActionPrefix != "" {
		// The pattern above admits "_", a LIKE wildcard: escape it.
		escaped := strings.NewReplacer(`\`, `\\`, `_`, `\_`).Replace(f.ActionPrefix)
		add(`action LIKE $%d ESCAPE '\'`, escaped+"%")
	}
	if f.Outcome != "" {
		add("outcome = $%d", string(f.Outcome))
	}
	if f.ResourceType != "" {
		add("resource_type = $%d", f.ResourceType)
	}
	return where, args
}

// GroupBy names what CountBy groups rows by.
type GroupBy string

const (
	// ByAction groups by the action name.
	ByAction GroupBy = "action"
	// ByReason groups by details->>'reason' (a rejected login's cause).
	ByReason GroupBy = "reason"
)

// Count is one group of CountBy: how many rows, and when the last one was.
type Count struct {
	Key     string
	Count   int
	FirstAt time.Time
	LastAt  time.Time
}

// CountBy counts the rows matching f per group, most frequent first (at
// most 50 groups) — the admin failures report's summary of what went
// wrong, without reading the rows one by one.
func CountBy(ctx context.Context, db Querier, f Filter, by GroupBy) ([]Count, error) {
	if db == nil || f.Validate() != nil {
		return nil, ErrInvalidEntry
	}
	expr := "action"
	switch by {
	case ByAction:
	case ByReason:
		expr = "COALESCE(details->>'reason', '')"
	default:
		return nil, ErrInvalidEntry
	}
	where, args := f.clauses(0)
	query := "SELECT " + expr + " AS k, count(*), min(at), max(at) FROM audit_log"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " GROUP BY k ORDER BY count(*) DESC, k LIMIT 50"
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	out := []Count{}
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.Key, &c.Count, &c.FirstAt, &c.LastAt); err != nil {
			return nil, ErrStorage
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrStorage
	}
	return out, nil
}
