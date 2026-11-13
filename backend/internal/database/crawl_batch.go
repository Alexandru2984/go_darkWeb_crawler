package database

import (
	"context"
	"database/sql"
	"errors"
	"sort"
)

var ErrCrawlBatch = errors.New("send between 1 and 20 onion URLs")

type CrawlBatchItem struct {
	Line   int    `json:"line"`
	URL    string `json:"url,omitempty"`
	Result string `json:"result"`
}
type CrawlBatchResult struct {
	Items    []CrawlBatchItem `json:"items"`
	Added    int              `json:"added"`
	Skipped  int              `json:"skipped"`
	QuotaHit bool             `json:"quota_hit"`
	Preview  bool             `json:"preview"`
	Error    string           `json:"error,omitempty"`
}

func (db *DB) CrawlBatch(ctx context.Context, uid int, raw []string, maxPending int, preview bool) (CrawlBatchResult, error) {
	result := CrawlBatchResult{Items: make([]CrawlBatchItem, 0, 20), Preview: preview}
	if len(raw) < 1 || len(raw) > 20 {
		return result, ErrCrawlBatch
	}
	urls := make([]string, len(raw))
	hosts := make([]string, len(raw))
	uniqueHosts := map[string]bool{}
	for i, s := range raw {
		url, host, err := canonicalCrawlURL(s)
		if err == nil {
			urls[i] = url
			hosts[i] = host
			uniqueHosts[host] = true
		}
	}
	options := &sql.TxOptions{}
	if preview {
		options.ReadOnly = true
		options.Isolation = sql.LevelRepeatableRead
	}
	tx, err := db.Conn.BeginTx(ctx, options)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if !preview {
		// Sort the actual advisory keys, not host strings: distinct hostnames
		// can share a 32-bit hash and otherwise invert another batch's order.
		keys := make(map[int64]bool, len(uniqueHosts))
		for host := range uniqueHosts {
			var key int64
			if err := tx.QueryRowContext(ctx, `SELECT hashtext($1)::bigint`, host).Scan(&key); err != nil {
				return result, err
			}
			keys[key] = true
		}
		ordered := make([]int64, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
		for _, key := range ordered {
			if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, key); err != nil {
				return result, err
			}
		}
		if err := lockUserQuota(tx, uid); err != nil {
			return result, err
		}
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM nodes WHERE user_id=$1 AND processing_status='pending'`, uid).Scan(&pending); err != nil {
		return result, err
	}
	seen := map[string]bool{}
	for i, url := range urls {
		item := CrawlBatchItem{Line: i + 1, URL: url, Result: "invalid"}
		if url != "" {
			if seen[url] {
				item.Result = "duplicate"
			} else {
				seen[url] = true
				blocked, err := domainBlacklistedTx(tx, hosts[i])
				if err != nil {
					return result, err
				}
				var exists bool
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM nodes WHERE user_id=$1 AND url=$2)`, uid, url).Scan(&exists); err != nil {
					return result, err
				}
				switch {
				case blocked:
					item.Result = "blocked"
				case exists:
					item.Result = "already_exists"
				case maxPending > 0 && pending >= maxPending:
					item.Result = "quota_full"
					result.QuotaHit = true
				default:
					item.Result = "accepted"
					if !preview {
						r, err := tx.ExecContext(ctx, `INSERT INTO nodes(url,host,user_id,depth,processing_status) VALUES($1,$2,$3,0,'pending') ON CONFLICT(url,user_id) DO NOTHING`, url, hosts[i], uid)
						if err != nil {
							return result, err
						}
						n, err := r.RowsAffected()
						if err != nil {
							return result, err
						}
						if n == 0 {
							item.Result = "already_exists"
						}
					}
					if item.Result == "accepted" {
						pending++
						result.Added++
					}
				}
			}
		}
		if item.Result != "accepted" {
			result.Skipped++
		}
		result.Items = append(result.Items, item)
	}
	if result.QuotaHit && !preview {
		result.Error = "The queue submission limit was reached. Check added, skipped and individual results before retrying."
	}
	return result, tx.Commit()
}
