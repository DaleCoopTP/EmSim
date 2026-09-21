package reporting

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"time"

	"emsim/internal/media"
	"emsim/internal/platform/tasks"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Builder struct {
	store ArtifactStore
	tasks *tasks.Store
	root  string
}

func NewBuilder(store ArtifactStore, taskStore *tasks.Store, root string) *Builder {
	return &Builder{store: store, tasks: taskStore, root: root}
}

func (b *Builder) Handle(ctx context.Context, lease tasks.Lease) error {
	var payload buildPayload
	if err := json.Unmarshal(lease.Payload, &payload); err != nil || payload.ReportFileID == uuid.Nil {
		return errors.New("report.build: invalid payload")
	}
	file, err := b.store.ReportFileByID(ctx, payload.ReportFileID)
	if err != nil {
		return err
	}
	if file.TaskID != lease.TaskID {
		return errors.New("report.build: task mismatch")
	}
	var snapshot Snapshot
	if err := json.Unmarshal(file.Basis, &snapshot); err != nil {
		return errors.New("report.build: invalid snapshot")
	}
	generatedAt := time.Now().UTC()
	body, err := RenderPDF(snapshot, generatedAt)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(body)
	files, err := media.NewFileStore(b.root)
	if err != nil {
		return err
	}
	if err := files.Put(ctx, bytes.NewReader(body), digest, int64(len(body)), 20<<20); err != nil {
		return err
	}
	return b.store.WithTx(ctx, func(tx pgx.Tx) error {
		blob, err := b.store.InsertBlob(ctx, tx, Blob{ID: uuid.New(), SHA256: digest, MIME: "application/pdf", Size: int64(len(body))})
		if err != nil {
			return err
		}
		if err := b.store.MarkReportReady(ctx, tx, file.ID, blob, generatedAt); err != nil {
			return err
		}
		result, _ := json.Marshal(map[string]string{"blob_id": blob.ID.String(), "report_file_id": file.ID.String()})
		_, err = b.tasks.Terminal(ctx, tx, tasks.TerminalRequest{Lease: lease, Now: generatedAt, Outcome: tasks.Done(result)})
		return err
	})
}

func NewBuilderFromEnvironment(store ArtifactStore, taskStore *tasks.Store) *Builder {
	return NewBuilder(store, taskStore, os.Getenv("BLOB_ROOT"))
}
