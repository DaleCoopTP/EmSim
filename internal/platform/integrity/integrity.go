// Package integrity is the administrator's data integrity check
// (ADR-038): does what is stored still match what was recorded about it.
// It only reads. The generic runner and the checks that touch nothing
// but platform state live here; the checks that need another module's
// rules (how a blob file is addressed, how an evidence digest is
// canonicalized) are composed in cmd/emsim and handed in as Checks.
package integrity

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"emsim/internal/platform/backup"

	"github.com/jackc/pgx/v5"
)

// MaxExamples bounds the ids a section lists: enough to start an
// investigation, not a dump of the damage.
const MaxExamples = 20

// Section is one check's outcome. Examples are ids (or copy names) only —
// never content. A check that could not run says so in Error rather than
// counting as clean.
type Section struct {
	Name     string   `json:"name"`
	Checked  int      `json:"checked"`
	Problems int      `json:"problems"`
	Examples []string `json:"examples"`
	Error    string   `json:"error,omitempty"`
}

// Report is one full run.
type Report struct {
	At       time.Time `json:"at"`
	Sections []Section `json:"sections"`
}

// Problems is the total mismatch count over every section.
func (r Report) Problems() int {
	total := 0
	for _, s := range r.Sections {
		total += s.Problems
	}
	return total
}

// OK is true when every check ran and none found a mismatch.
func (r Report) OK() bool {
	for _, s := range r.Sections {
		if s.Problems > 0 || s.Error != "" {
			return false
		}
	}
	return len(r.Sections) > 0
}

// Result is what a check returns: how many things it looked at, the ids
// of those that did not match (Collector caps the list), and their count.
type Result struct {
	Checked  int
	Problems int
	Examples []string
}

// Add records one mismatch.
func (r *Result) Add(id string) {
	r.Problems++
	if len(r.Examples) < MaxExamples {
		r.Examples = append(r.Examples, id)
	}
}

// Check is one named verification. It must respect ctx.
type Check struct {
	Name string
	Run  func(ctx context.Context) (Result, error)
}

// Run executes the checks in order. A failing check is reported, not
// fatal: the others still run.
func Run(ctx context.Context, now time.Time, checks []Check) Report {
	report := Report{At: now.UTC()}
	for _, check := range checks {
		section := Section{Name: check.Name, Examples: []string{}}
		result, err := check.Run(ctx)
		if err != nil {
			section.Error = "check_failed"
		}
		section.Checked, section.Problems = result.Checked, result.Problems
		if result.Examples != nil {
			section.Examples = result.Examples
		}
		report.Sections = append(report.Sections, section)
		if ctx.Err() != nil {
			break
		}
	}
	return report
}

// Recompute derives the digest a stored body should carry.
type Recompute func(body []byte) ([32]byte, error)

// RowDigestCheck scans `sql`, which must select (id text, body jsonb,
// digest bytea), and counts the rows whose digest is not what recompute
// gives for their body.
func RowDigestCheck(name string, db interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}, sql string, recompute Recompute) Check {
	return Check{Name: name, Run: func(ctx context.Context) (Result, error) {
		var result Result
		rows, err := db.Query(ctx, sql)
		if err != nil {
			return result, err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var body, digest []byte
			if err := rows.Scan(&id, &body, &digest); err != nil {
				return result, err
			}
			result.Checked++
			got, err := recompute(body)
			if err != nil || len(digest) != len(got) || string(digest) != string(got[:]) {
				result.Add(id)
			}
		}
		return result, rows.Err()
	}}
}

// SchemaCheck compares the applied migration version with what this build
// expects.
func SchemaCheck(current func(context.Context) (int64, error), expected int64) Check {
	return Check{Name: "schema", Run: func(ctx context.Context) (Result, error) {
		version, err := current(ctx)
		if err != nil {
			return Result{}, err
		}
		result := Result{Checked: 1}
		if version != expected {
			result.Add(fmt.Sprintf("db=%d expected=%d", version, expected))
		}
		return result, nil
	}}
}

// BackupCheck verifies the newest complete copy in dir against its
// manifest (older ones are rotated out and not worth the read). No copies
// at all is not a mismatch — Checked stays 0 and the status screen shows
// "no backups" on its own.
func BackupCheck(dir string) Check {
	return Check{Name: "backup", Run: func(ctx context.Context) (Result, error) {
		var result Result
		copies, err := backup.List(dir)
		if err != nil {
			return result, err
		}
		if len(copies) == 0 {
			return result, nil
		}
		result.Checked = 1
		if _, err := backup.Verify(filepath.Join(dir, copies[0].Name)); err != nil {
			result.Add(copies[0].Name)
		}
		return result, nil
	}}
}
