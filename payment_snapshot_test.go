package snapshot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestBuildSnapshotRiskDecision(t *testing.T) {
	events := []PaymentEvent{
		{EventID: "pay-003", RiskScore: 80},
		{EventID: "pay-001", RiskScore: 12},
		{EventID: "pay-002", RiskScore: 50},
	}
	want := []RiskAction{ActionAllow, ActionReview, ActionBlock}
	snapshot := BuildSnapshot("2026-09-03", time.Date(2026, 9, 4, 1, 0, 0, 0, time.UTC), events)
	for i, action := range want {
		if snapshot.Audit[i].Action != action {
			t.Fatalf("audit[%d] action = %q, want %q", i, snapshot.Audit[i].Action, action)
		}
	}
}

func TestSnapshotJobWritesStablePartition(t *testing.T) {
	store := &recordingStore{}
	job := SnapshotJob{
		Store: store, Bucket: "fintech-nightly-snapshots",
		Now: func() time.Time { return time.Date(2026, 9, 4, 1, 0, 0, 0, time.UTC) },
	}
	key, err := job.Run(context.Background(), "2026-09-03", []PaymentEvent{{EventID: "pay-001", RiskScore: 81}})
	if err != nil {
		t.Fatal(err)
	}
	if key != "payments/date=2026-09-03/snapshot.json" || store.bucket != "fintech-nightly-snapshots" {
		t.Fatalf("write target = %s/%s", store.bucket, key)
	}
	raw, err := base64.StdEncoding.DecodeString(store.dataBase64)
	if err != nil {
		t.Fatal(err)
	}
	var got NightlySnapshot
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Audit[0].Action != ActionBlock || store.idempotencyKey == "" {
		t.Fatalf("audit action = %q, idempotency key present = %t", got.Audit[0].Action, store.idempotencyKey != "")
	}
}

type recordingStore struct {
	bucket         string
	dataBase64     string
	idempotencyKey string
}

func (s *recordingStore) EnsureBucket(_ context.Context, bucket string) error {
	s.bucket = bucket
	return nil
}

func (s *recordingStore) PutObject(_ context.Context, bucket, _ string, dataBase64, idempotencyKey string) error {
	s.bucket = bucket
	s.dataBase64 = dataBase64
	s.idempotencyKey = idempotencyKey
	return nil
}
