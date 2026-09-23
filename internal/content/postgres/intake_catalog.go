package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"emsim/internal/content"

	"github.com/jackc/pgx/v5"
)

func scanIntakeCatalog(row pgx.Row) (content.IntakeCatalog, error) {
	var data []byte
	if err := row.Scan(&data); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return content.IntakeCatalog{}, content.ErrNotFound
		}
		return content.IntakeCatalog{}, content.ErrStorage
	}
	var c content.IntakeCatalog
	if err := json.Unmarshal(data, &c); err != nil {
		return content.IntakeCatalog{}, content.ErrStorage
	}
	return c, nil
}

func (s *Store) LatestIntakeCatalog(ctx context.Context, tx pgx.Tx) (content.IntakeCatalog, error) {
	return scanIntakeCatalog(tx.QueryRow(ctx, `SELECT definition FROM intake_catalog_versions ORDER BY version DESC LIMIT 1`))
}

func (s *Store) IntakeCatalogByVersion(ctx context.Context, tx pgx.Tx, version int) (content.IntakeCatalog, error) {
	return scanIntakeCatalog(tx.QueryRow(ctx, `SELECT definition FROM intake_catalog_versions WHERE version=$1`, version))
}

func (s *Store) InsertIntakeCatalog(ctx context.Context, tx pgx.Tx, c content.IntakeCatalog) error {
	data, err := json.Marshal(c)
	if err != nil {
		return content.ErrStorage
	}
	if _, err := tx.Exec(ctx, `INSERT INTO intake_catalog_versions (version, definition) VALUES ($1,$2)`, c.Version, data); err != nil {
		return content.ErrStorage
	}
	return nil
}
