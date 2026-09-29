package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"strconv"

	"emsim/internal/platform/audit"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Bulk user creation (ADR-038): an administrator loads a class as a table.
// The whole file is created or nothing is; the server invents every
// password, shows it once in the response and keeps only its hash.

// MaxImportRows bounds one file: each row costs one argon2id hash.
const MaxImportRows = 300

// ImportRow is one line of the file; Row is its 1-based number among the
// data rows (the header is not counted), for the error report.
type ImportRow struct {
	Row         int
	Login       string
	FullName    string
	Role        Role
	ServiceCode *string
}

// ImportedUser is one created (or, in a dry run, creatable) user. Password
// is set only when users were really created.
type ImportedUser struct {
	Row         int
	Login       string
	FullName    string
	Role        Role
	ServiceCode *string
	Password    string
}

// ImportIssue names one problem with one row.
type ImportIssue struct {
	Row    int
	Field  string
	Reason string
}

// ImportError is the whole report of a file that cannot be loaded: every
// problem found, not only the first, so the file can be fixed in one pass.
type ImportError struct{ Issues []ImportIssue }

func (e *ImportError) Error() string { return "user import has problems" }

// maxImportIssues bounds the report.
const maxImportIssues = 50

// passwordAlphabet leaves out the characters that are easy to misread on a
// printed sheet (0/O, 1/l/I).
const passwordAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// generatePassword draws length characters uniformly from the alphabet.
func generatePassword(length int) (string, error) {
	out := make([]byte, length)
	max := big.NewInt(int64(len(passwordAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = passwordAlphabet[n.Int64()]
	}
	return string(out), nil
}

// ImportUsers checks every row and, unless dryRun, creates them all in one
// transaction with one audit row. The passwords are returned once and
// never stored or logged.
func (s *Service) ImportUsers(ctx context.Context, rows []ImportRow, dryRun bool, actor Principal, requestID string) ([]ImportedUser, error) {
	if len(rows) == 0 {
		return nil, &ImportError{Issues: []ImportIssue{{Row: 0, Field: "file", Reason: "empty"}}}
	}
	if len(rows) > MaxImportRows {
		return nil, &ImportError{Issues: []ImportIssue{{Row: 0, Field: "file", Reason: "too_many_rows"}}}
	}

	var issues []ImportIssue
	add := func(row int, field, reason string) {
		if len(issues) < maxImportIssues {
			issues = append(issues, ImportIssue{Row: row, Field: field, Reason: reason})
		}
	}
	seen := map[string]int{}
	knownServices := map[string]bool{}
	for _, r := range rows {
		if err := ValidateLogin(r.Login); err != nil {
			add(r.Row, "login", "invalid")
		} else if first, dup := seen[r.Login]; dup {
			add(r.Row, "login", "duplicate_of_row_"+strconv.Itoa(first))
		} else {
			seen[r.Login] = r.Row
		}
		if err := ValidateFullName(r.FullName); err != nil {
			var ve *ValidationError
			if errors.As(err, &ve) {
				add(r.Row, "full_name", ve.Reason)
			}
		}
		if !r.Role.Valid() {
			add(r.Row, "role", "invalid")
			continue
		}
		if err := validateServiceCode(r.Role, r.ServiceCode); err != nil {
			var ve *ValidationError
			if errors.As(err, &ve) {
				add(r.Row, "service_code", ve.Reason)
			}
			continue
		}
		if r.ServiceCode != nil && *r.ServiceCode != "" && s.catalog != nil {
			code := *r.ServiceCode
			ok, checked := knownServices[code]
			if !checked {
				exists, err := s.catalog.ServiceExists(ctx, code)
				if err != nil {
					return nil, ErrStorage
				}
				knownServices[code], ok = exists, exists
			}
			if !ok {
				add(r.Row, "service_code", "unknown")
			}
		}
	}

	// Logins that already exist are found in the database, not by racing
	// the insert, so one report covers the whole file.
	takenErr := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		for _, r := range rows {
			if ValidateLogin(r.Login) != nil {
				continue
			}
			if _, err := s.store.UserByLogin(ctx, tx, r.Login); err == nil {
				add(r.Row, "login", "taken")
			} else if !errors.Is(err, ErrNotFound) {
				return err
			}
		}
		return nil
	})
	if takenErr != nil {
		return nil, takenErr
	}
	if len(issues) > 0 {
		return nil, &ImportError{Issues: issues}
	}

	length := 14
	if s.policy.PasswordMinLength > length {
		length = s.policy.PasswordMinLength
	}
	out := make([]ImportedUser, len(rows))
	candidates := make([]User, len(rows))
	for i, r := range rows {
		out[i] = ImportedUser{Row: r.Row, Login: r.Login, FullName: r.FullName, Role: r.Role, ServiceCode: r.ServiceCode}
		if dryRun {
			continue
		}
		password, err := generatePassword(length)
		if err != nil {
			return nil, ErrStorage
		}
		hash, err := HashPassword(password, DefaultParams)
		if err != nil {
			return nil, ErrStorage
		}
		out[i].Password = password
		candidates[i] = User{
			ID: uuid.New(), Login: r.Login, PasswordHash: hash, FullName: r.FullName, Role: r.Role,
			ServiceCode: r.ServiceCode, Level: LevelEasy, Active: true, MustChangePassword: s.policy.forcesChange(r.Role),
		}
	}
	if dryRun {
		return out, nil
	}

	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		for _, u := range candidates {
			if _, err := s.store.InsertUser(ctx, tx, u); err != nil {
				return err
			}
		}
		return s.store.AuditRecord(ctx, tx, audit.Entry{
			ActorID: &actor.UserID, ActorRole: string(actor.Role), Action: "admin.user.import",
			ResourceType: "user", Outcome: audit.OutcomeOK, RequestID: requestID,
			Details: map[string]any{"count": len(candidates)},
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
