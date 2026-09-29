package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"emsim/internal/content"
	"emsim/internal/media"
	"emsim/internal/platform/audit"
	"emsim/internal/platform/config"
	"emsim/internal/platform/integrity"
	pgstore "emsim/internal/platform/postgres"
	"emsim/internal/platform/status"
	"emsim/internal/platform/tasks"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// kindIntegrityCheck (ADR-038) verifies that stored data still matches
// what was recorded about it: blob files against their hashes, sealed
// evidence and approved scenario bodies against their digests, the newest
// backup copy against its manifest, and the schema version. It only
// reads; a mismatch is reported (audit, status screen), never repaired.
const kindIntegrityCheck tasks.Kind = "integrity.check"

// integrityCheckAt is the local time the daily check is enqueued: after
// the nightly backup (03:00) and audit cleanup (04:00).
const integrityCheckAt = 5 * time.Hour

// integrityChecks composes the checks over this process's own storage.
func integrityChecks(pool *pgxpool.Pool, processConfig config.Worker) []integrity.Check {
	checks := []integrity.Check{integrity.SchemaCheck(func(ctx context.Context) (int64, error) {
		return pgstore.CurrentVersion(ctx, pool)
	}, pgstore.ExpectedSchemaVersion)}
	if root := os.Getenv("BLOB_ROOT"); root != "" {
		checks = append(checks, blobsCheck(pool, root))
	}
	checks = append(checks,
		integrity.RowDigestCheck("evidence", pool, `SELECT item_id::text, body, digest FROM evidence`, bodyDigest),
		integrity.RowDigestCheck("scenario_versions", pool, `SELECT id::text, body, digest FROM scenario_versions`, bodyDigest),
	)
	if processConfig.BackupDir != "" {
		checks = append(checks, integrity.BackupCheck(processConfig.BackupDir))
	}
	return checks
}

// bodyDigest is how evidence and scenario versions were sealed: sha256 of
// the canonical form of the jsonb body (training.canonicalDigest and
// content.BodyDigest use the same content.Digest).
func bodyDigest(body []byte) ([32]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return [32]byte{}, err
	}
	return content.Digest(tree), nil
}

// blobsCheck reads every registered blob file back: it must exist, have
// the recorded size and hash to the recorded sha256.
func blobsCheck(pool *pgxpool.Pool, root string) integrity.Check {
	return integrity.Check{Name: "blobs", Run: func(ctx context.Context) (integrity.Result, error) {
		var result integrity.Result
		store, err := media.NewFileStore(root)
		if err != nil {
			return result, err
		}
		type blob struct {
			id   string
			sha  [32]byte
			size int64
		}
		rows, err := pool.Query(ctx, `SELECT id::text, sha256, size FROM blobs ORDER BY created_at`)
		if err != nil {
			return result, err
		}
		var blobs []blob
		for rows.Next() {
			var b blob
			var sha []byte
			if err := rows.Scan(&b.id, &sha, &b.size); err != nil || len(sha) != len(b.sha) {
				rows.Close()
				return result, errors.New("blob row is malformed")
			}
			copy(b.sha[:], sha)
			blobs = append(blobs, b)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return result, err
		}
		for _, b := range blobs {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			result.Checked++
			if !blobMatches(store, b.sha, b.size) {
				result.Add(b.id)
			}
		}
		return result, nil
	}}
}

func blobMatches(store *media.FileStore, sha [32]byte, size int64) bool {
	f, err := store.Open(sha)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil || n != size {
		return false
	}
	return bytes.Equal(h.Sum(nil), sha[:])
}

// integrityHandler is kindIntegrityCheck's worker side. The run reads
// outside any transaction; its audit row and the task's terminal state
// then commit together. Only ids and counts leave the check.
func integrityHandler(pool *pgxpool.Pool, store *tasks.Store, processConfig config.Worker) tasks.Handler {
	return tasks.HandlerFunc(func(ctx context.Context, lease tasks.Lease) error {
		report := integrity.Run(ctx, time.Now(), integrityChecks(pool, processConfig))
		if ctx.Err() != nil {
			failure, ferr := tasks.NewHandlerFailure(tasks.Retryable, "integrity_interrupted")
			if ferr != nil {
				return ferr
			}
			return failure
		}
		result, err := json.Marshal(report)
		if err != nil {
			return errors.New("integrity.check result encoding failed")
		}
		outcome := audit.OutcomeOK
		if !report.OK() {
			outcome = audit.OutcomeError
		}
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			return errors.New("integrity.check transaction failed")
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := audit.Record(ctx, tx, audit.Entry{
			ActorRole: "system", Action: "integrity.check", ResourceType: "integrity", Outcome: outcome,
			Details: map[string]any{"problems": report.Problems(), "sections": len(report.Sections)},
		}); err != nil {
			return errors.New("integrity.check record failed")
		}
		if _, err := store.Terminal(ctx, tx, tasks.TerminalRequest{
			Lease: lease, Now: time.Now().UTC(), Outcome: tasks.Done(result),
		}); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return errors.New("integrity.check commit failed")
		}
		heartbeat := status.StatusOK
		if !report.OK() {
			heartbeat = status.StatusUnavailable
		}
		var detail map[string]any
		if err := json.Unmarshal(result, &detail); err == nil {
			_ = status.NewStore(pool).UpsertHeartbeat(ctx, status.ComponentIntegrity, heartbeat, detail)
		}
		return nil
	})
}
