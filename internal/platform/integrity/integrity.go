package integrity

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"emsim/internal/platform/backup"

	"github.com/jackc/pgx/v5"
)

const MaxExamples = 20

type Section struct {
	Name     string   `json:"name"`
	Checked  int      `json:"checked"`
	Problems int      `json:"problems"`
	Examples []string `json:"examples"`
	Error    string   `json:"error,omitempty"`
}

type Report struct {
	At       time.Time `json:"at"`
	Sections []Section `json:"sections"`
}

func (r Report) Problems() int {
	total := 0
	for _, s := range r.Sections {
		total += s.Problems
	}
	return total
}

func (r Report) OK() bool {
	for _, s := range r.Sections {
		if s.Problems > 0 || s.Error != "" {
			return false
		}
	}
	return len(r.Sections) > 0
}

type Result struct {
	Checked  int
	Problems int
	Examples []string
}

func (r *Result) Add(id string) {
	r.Problems++
	if len(r.Examples) < MaxExamples {
		r.Examples = append(r.Examples, id)
	}
}

type Check struct {
	Name string
	Run  func(ctx context.Context) (Result, error)
}

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

type Recompute func(body []byte) ([32]byte, error)

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
