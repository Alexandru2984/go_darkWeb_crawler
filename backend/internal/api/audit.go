package api

import (
	"onion-spider/internal/auth"
	"onion-spider/internal/database"
)

// auditEmailReference and auditIPReference keep raw PII in request memory only.
// PostgreSQL receives keyed references that still support short-window auth
// throttling without turning auth_audit (and every backup) into an identity and
// network-history table.
func auditEmailReference(email string) string {
	return auth.AuditReference("email", database.NormalizeEmail(email))
}

func auditIPReference(ip string) string {
	return auth.AuditReference("ip", ip)
}

func (d *deps) logAuthEvent(event, email, ip string) {
	d.cfg.DB.LogAuthEvent(event, auditEmailReference(email), auditIPReference(ip))
}

func (d *deps) countRecentAuthEvents(event, email string, windowMinutes int) (int, error) {
	return d.cfg.DB.CountRecentAuthEvents(event, auditEmailReference(email), windowMinutes)
}
