package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxCollections = 100

var (
	ErrCollectionName      = errors.New("collection names must have 1–40 characters without control characters")
	ErrCollectionLimit     = errors.New("collection limit reached (maximum 100 per account)")
	ErrCollectionMissing   = errors.New("collection not found")
	ErrCollectionDuplicate = errors.New("a collection with this name already exists")
)

// Collections are the account's existing tags, without a second membership store.
func NormalizeCollectionName(name string) (string, error) {
	name = NormalizeTag(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxTagLen || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", ErrCollectionName
	}
	return name, nil
}

func lockCollectionAccount(ctx context.Context, tx *sql.Tx, userID int) error {
	var id int
	err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCollectionMissing
	}
	return err
}

func (db *DB) AddTagContext(ctx context.Context, nodeURL string, userID int, tag string) error {
	tag, err := NormalizeCollectionName(tag)
	if err != nil {
		return err
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockCollectionAccount(ctx, tx, userID); err != nil {
		return err
	}
	nodeID, err := nodeIDFor(tx, nodeURL, userID)
	if err != nil {
		return err
	}
	// Hold the referenced row until commit to serialize with crawl erasure.
	var held int
	if err := tx.QueryRowContext(ctx, `SELECT id FROM nodes WHERE id=$1 AND user_id=$2 FOR KEY SHARE`, nodeID, userID).Scan(&held); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNodeNotFound
		}
		return err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM node_tags WHERE user_id=$1 AND node_id=$2 AND tag=$3)`, userID, nodeID, tag).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return tx.Commit()
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM node_tags WHERE user_id=$1 AND node_id=$2`, userID, nodeID).Scan(&count); err != nil {
		return err
	}
	if count >= MaxTagsPerNode {
		return ErrTooManyTags
	}
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM node_tags WHERE user_id=$1 AND tag=$2)`, userID, tag).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if err := tx.QueryRowContext(ctx, `SELECT count(DISTINCT tag) FROM node_tags WHERE user_id=$1`, userID).Scan(&count); err != nil {
			return err
		}
		if count >= MaxCollections {
			return ErrCollectionLimit
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO node_tags(user_id,node_id,tag) VALUES($1,$2,$3) ON CONFLICT(user_id,node_id,tag) DO NOTHING`, userID, nodeID, tag); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *DB) RenameCollection(ctx context.Context, userID int, oldName, newName string) (int64, error) {
	oldName, err := NormalizeCollectionName(oldName)
	if err != nil {
		return 0, err
	}
	newName, err = NormalizeCollectionName(newName)
	if err != nil {
		return 0, err
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := lockCollectionAccount(ctx, tx, userID); err != nil {
		return 0, err
	}
	var count int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM node_tags WHERE user_id=$1 AND tag=$2`, userID, oldName).Scan(&count); err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, ErrCollectionMissing
	}
	if oldName != newName {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM node_tags WHERE user_id=$1 AND tag=$2)`, userID, newName).Scan(&exists); err != nil {
			return 0, err
		}
		if exists {
			return 0, ErrCollectionDuplicate
		}
		if _, err := tx.ExecContext(ctx, `UPDATE node_tags SET tag=$3 WHERE user_id=$1 AND tag=$2`, userID, oldName, newName); err != nil {
			return 0, err
		}
		// Saved preferences follow an intentional collection rename.
		if _, err := tx.ExecContext(ctx, `UPDATE saved_searches SET collection_tag=$3,updated_at=CURRENT_TIMESTAMP WHERE user_id=$1 AND collection_tag=$2`, userID, oldName, newName); err != nil {
			return 0, err
		}
	}
	return count, tx.Commit()
}

func (db *DB) DeleteCollection(ctx context.Context, userID int, tag string) (int64, error) {
	tag, err := NormalizeCollectionName(tag)
	if err != nil {
		return 0, err
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := lockCollectionAccount(ctx, tx, userID); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM node_tags WHERE user_id=$1 AND tag=$2`, userID, tag)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, ErrCollectionMissing
	}
	// Keep saved criteria restricted: a deleted collection must yield no sites,
	// never silently broaden a saved query to the whole account.
	return n, tx.Commit()
}

func (db *DB) PurgeCollections(ctx context.Context, userID int) (int64, error) {
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := lockCollectionAccount(ctx, tx, userID); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM node_tags WHERE user_id=$1`, userID)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}
