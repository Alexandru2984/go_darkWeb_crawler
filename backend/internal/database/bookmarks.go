package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxBookmarks = 1000

var (
	ErrBookmarkLimit   = errors.New("bookmark limit reached (maximum 1000 per account)")
	ErrBookmarkMissing = errors.New("bookmark not found")
	ErrReviewState     = errors.New("choose unreviewed, reviewed or follow_up")
	ErrNoteText        = errors.New("notes support up to 4000 Unicode characters and cannot contain NUL")
)

type BookmarkReference struct {
	NodeID    int       `json:"node_id"`
	URL       string    `json:"url"`
	Review    string    `json:"review"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func NormalizeNoteText(body string) (string, error) {
	body = strings.TrimSpace(body)
	if !utf8.ValidString(body) || utf8.RuneCountInString(body) > MaxNoteLen || strings.ContainsRune(body, 0) {
		return "", ErrNoteText
	}
	return body, nil
}

func validReviewState(value string) bool {
	return value == "unreviewed" || value == "reviewed" || value == "follow_up"
}

func (db *DB) BookmarkForNode(ctx context.Context, userID, nodeID int) (*BookmarkReference, error) {
	var ref BookmarkReference
	err := db.Conn.QueryRowContext(ctx, `SELECT b.node_id,n.url,b.review_state,b.created_at,b.updated_at FROM node_bookmarks b JOIN nodes n ON n.id=b.node_id AND n.user_id=b.user_id WHERE b.user_id=$1 AND b.node_id=$2`, userID, nodeID).Scan(&ref.NodeID, &ref.URL, &ref.Review, &ref.CreatedAt, &ref.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ref, nil
}

func (db *DB) SetBookmark(ctx context.Context, userID int, nodeURL string, remove bool) error {
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockCollectionAccount(ctx, tx, userID); err != nil {
		return err
	}
	id, err := nodeIDFor(tx, nodeURL, userID)
	if err != nil {
		return err
	}
	var held int
	if err := tx.QueryRowContext(ctx, `SELECT id FROM nodes WHERE user_id=$1 AND id=$2 FOR KEY SHARE`, userID, id).Scan(&held); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNodeNotFound
		}
		return err
	}
	if remove {
		if _, err := tx.ExecContext(ctx, `DELETE FROM node_bookmarks WHERE user_id=$1 AND node_id=$2`, userID, id); err != nil {
			return err
		}
		return tx.Commit()
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM node_bookmarks WHERE user_id=$1 AND node_id=$2)`, userID, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return tx.Commit()
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM node_bookmarks WHERE user_id=$1`, userID).Scan(&count); err != nil {
		return err
	}
	if count >= MaxBookmarks {
		return ErrBookmarkLimit
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO node_bookmarks(user_id,node_id) VALUES($1,$2)`, userID, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *DB) SetBookmarkReview(ctx context.Context, userID int, nodeURL, review string) error {
	if !validReviewState(review) {
		return ErrReviewState
	}
	id, err := nodeIDFor(db.Conn, nodeURL, userID)
	if err != nil {
		return err
	}
	result, err := db.Conn.ExecContext(ctx, `UPDATE node_bookmarks SET review_state=$3,updated_at=CURRENT_TIMESTAMP WHERE user_id=$1 AND node_id=$2`, userID, id, review)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrBookmarkMissing
	}
	return nil
}

func (db *DB) ListBookmarkReferences(ctx context.Context, userID int) ([]BookmarkReference, error) {
	rows, err := db.Conn.QueryContext(ctx, `SELECT b.node_id,n.url,b.review_state,b.created_at,b.updated_at FROM node_bookmarks b JOIN nodes n ON n.id=b.node_id AND n.user_id=b.user_id WHERE b.user_id=$1 ORDER BY b.created_at DESC,b.node_id DESC LIMIT $2`, userID, MaxBookmarks)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := make([]BookmarkReference, 0)
	for rows.Next() {
		var ref BookmarkReference
		if err := rows.Scan(&ref.NodeID, &ref.URL, &ref.Review, &ref.CreatedAt, &ref.UpdatedAt); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func (db *DB) PurgeBookmarks(ctx context.Context, userID int) (int64, error) {
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := lockCollectionAccount(ctx, tx, userID); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM node_bookmarks WHERE user_id=$1`, userID)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return count, tx.Commit()
}
