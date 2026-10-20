package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

type PaymentEvent struct {
	EventID       string    `json:"event_id"`
	AccountID     string    `json:"account_id"`
	AmountMinor   int64     `json:"amount_minor"`
	Currency      string    `json:"currency"`
	RiskScore     int       `json:"risk_score"`
	OccurredAtUTC time.Time `json:"occurred_at_utc"`
}

type RiskAction string

const (
	ActionAllow  RiskAction = "allow"
	ActionReview RiskAction = "review"
	ActionBlock  RiskAction = "block"
)

type AuditNotification struct {
	EventID string     `json:"event_id"`
	Action  RiskAction `json:"action"`
	Reason  string     `json:"reason"`
}

type NightlySnapshot struct {
	BusinessDate string              `json:"business_date"`
	GeneratedUTC time.Time           `json:"generated_utc"`
	Payments     []PaymentEvent      `json:"payments"`
	Audit        []AuditNotification `json:"audit_notifications"`
}

type ObjectStore interface {
	EnsureBucket(context.Context, string) error
	PutObject(context.Context, string, string, string, string) error
}

type SnapshotJob struct {
	Store  ObjectStore
	Bucket string
	Now    func() time.Time
}

func DecideRisk(score int) (RiskAction, string) {
	switch {
	case score >= 80:
		return ActionBlock, "risk score at or above 80"
	case score >= 50:
		return ActionReview, "risk score from 50 through 79"
	default:
		return ActionAllow, "risk score below 50"
	}
}

func BuildSnapshot(businessDate string, generated time.Time, events []PaymentEvent) NightlySnapshot {
	payments := append([]PaymentEvent(nil), events...)
	sort.Slice(payments, func(i, j int) bool { return payments[i].EventID < payments[j].EventID })
	audit := make([]AuditNotification, 0, len(payments))
	for _, event := range payments {
		action, reason := DecideRisk(event.RiskScore)
		audit = append(audit, AuditNotification{EventID: event.EventID, Action: action, Reason: reason})
	}
	return NightlySnapshot{BusinessDate: businessDate, GeneratedUTC: generated.UTC(), Payments: payments, Audit: audit}
}

func (j SnapshotJob) Run(ctx context.Context, businessDate string, events []PaymentEvent) (string, error) {
	if j.Store == nil || j.Now == nil || j.Bucket == "" {
		return "", fmt.Errorf("snapshot job is not configured")
	}
	if err := j.Store.EnsureBucket(ctx, j.Bucket); err != nil {
		return "", fmt.Errorf("prepare bucket: %w", err)
	}
	snapshot := BuildSnapshot(businessDate, j.Now(), events)
	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode snapshot: %w", err)
	}
	key := "payments/date=" + businessDate + "/snapshot.json"
	digest := sha256.Sum256(payload)
	idempotencyKey := "payment-snapshot:" + businessDate + ":" + hex.EncodeToString(digest[:])
	if err := j.Store.PutObject(ctx, j.Bucket, key, base64.StdEncoding.EncodeToString(payload), idempotencyKey); err != nil {
		return "", fmt.Errorf("write snapshot: %w", err)
	}
	return key, nil
}
