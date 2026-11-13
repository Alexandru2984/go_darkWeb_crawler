package database

import (
	"context"
	"errors"
	"fmt"
	"github.com/lib/pq"
	"strings"
)

var ErrWatchSelection = errors.New("select between 1 and 200 positive event IDs")

type WatchFeedInput struct {
	Kind       string `json:"kind"`
	UnreadOnly bool   `json:"unread_only"`
	Title      string `json:"title"`
	Host       string `json:"host"`
	Tag        string `json:"tag"`
}

func (db *DB) QueryWatchEvents(ctx context.Context, userID int, input WatchFeedInput, limit, offset int) ([]WatchEvent, error) {
	events := make([]WatchEvent, 0)
	err := db.streamWatchEvents(ctx, userID, input, limit, offset, func(e WatchEvent) error { events = append(events, e); return nil })
	return events, err
}
func (db *DB) ExportWatchEvents(ctx context.Context, userID int, fn func(WatchEvent) error) error {
	return db.streamWatchEvents(ctx, userID, WatchFeedInput{}, 0, 0, fn)
}
func (db *DB) streamWatchEvents(ctx context.Context, userID int, input WatchFeedInput, limit, offset int, fn func(WatchEvent) error) error {
	switch input.Kind {
	case "", WatchEventChanged, WatchEventUnreachable, WatchEventRecovered:
	default:
		return ErrResearchFilters
	}
	filters, err := NormalizeResearchFilters(ResearchFilters{Title: input.Title, Host: input.Host, Tag: input.Tag})
	if err != nil {
		return err
	}
	args := []any{userID}
	where := []string{"e.user_id=$1"}
	bind := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	if input.Kind != "" {
		where = append(where, "e.kind="+bind(input.Kind))
	}
	if input.UnreadOnly {
		where = append(where, "e.seen_at IS NULL")
	}
	if filters.Host != "" {
		where = append(where, "n.host="+bind(filters.Host))
	}
	if filters.Title != "" {
		literal := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(filters.Title)
		where = append(where, `n.title ILIKE `+bind("%"+literal+"%")+` ESCAPE '\'`)
	}
	if filters.Tag != "" {
		where = append(where, "EXISTS(SELECT 1 FROM node_tags t WHERE t.user_id=$1 AND t.node_id=n.id AND t.tag="+bind(filters.Tag)+")")
	}
	query := `SELECT e.id,n.url,COALESCE(n.title,''),e.kind,COALESCE(e.status_code,0),COALESCE(TO_CHAR(e.detected_at,'YYYY-MM-DD"T"HH24:MI:SS'),''),e.seen_at IS NOT NULL
 FROM watch_events e JOIN watches w ON w.id=e.watch_id AND w.user_id=e.user_id JOIN nodes n ON n.id=w.node_id AND n.user_id=w.user_id WHERE ` + strings.Join(where, " AND ") + " ORDER BY e.detected_at DESC,e.id DESC"
	if limit > 0 {
		query += " LIMIT " + bind(limit) + " OFFSET " + bind(offset)
	}
	rows, err := db.Conn.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var e WatchEvent
		if err := rows.Scan(&e.ID, &e.URL, &e.Title, &e.Kind, &e.StatusCode, &e.DetectedAt, &e.Seen); err != nil {
			return err
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return rows.Err()
}
func (db *DB) MarkSelectedWatchEventsSeen(ctx context.Context, userID int, ids []int) (int64, error) {
	if len(ids) == 0 || len(ids) > 200 {
		return 0, ErrWatchSelection
	}
	for _, id := range ids {
		if id <= 0 {
			return 0, ErrWatchSelection
		}
	}
	result, err := db.Conn.ExecContext(ctx, `UPDATE watch_events SET seen_at=CURRENT_TIMESTAMP WHERE user_id=$1 AND id=ANY($2) AND seen_at IS NULL`, userID, pq.Array(ids))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
func (db *DB) PurgeWatchEvents(ctx context.Context, userID int) (int64, error) {
	result, err := db.Conn.ExecContext(ctx, `DELETE FROM watch_events WHERE user_id=$1`, userID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
