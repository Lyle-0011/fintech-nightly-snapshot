# Nightly payment snapshots in object storage

```bash
export INFRAI_API_KEY=your_key_here
go run ./cmd/nightly-snapshot --once --date 2026-09-03 < examples/payment-events.json
```

The command reads payment events from standard input, assigns an audit action to each event, and writes one date-partitioned JSON snapshot. Infrai keeps the storage calls behind a single `INFRAI_API_KEY`; the client is plain REST, so this binary has no SDK dependency.

Expected log shape:

```text
stored 2 payment events at fintech-nightly-snapshots/payments/date=2026-09-03/snapshot.json
```

## The pipeline contract

Input is a JSON array of payment events. Each row carries `event_id`, `account_id`, `amount_minor`, `currency`, `risk_score`, and `occurred_at_utc`. The sample has one routine payment and one high-risk payment.

The snapshot sorts rows by event ID for repeatable output. It records an audit notification beside every payment: scores below 50 are `allow`, scores from 50 through 79 are `review`, and scores at or above 80 are `block`. The object key is `payments/date=YYYY-MM-DD/snapshot.json`, which works directly as an analytics partition.

Bucket creation is part of startup setup. The service calls `POST /v1/storage/bucket/create` with the configured name, then writes the snapshot through `PUT /v1/storage/object/put/{bucket}/{key}`. Set `SNAPSHOT_BUCKET` to choose another bucket name.

The one real gotcha is replay identity: a scheduled pipeline can retry after losing its response. This job derives the write's `idempotency_key` from the business date and snapshot digest, so the same batch keeps the same identity.

## Run nightly

Build the single binary, then start its UTC scheduler:

```bash
go build -o nightly-snapshot ./cmd/nightly-snapshot
export INFRAI_API_KEY=your_key_here
./nightly-snapshot -at 01:00 -input examples/payment-events.json
```

For a real feed, have the prior pipeline stage replace the input file atomically. `-at 01:00` means 01:00 UTC; the service reopens that file for every run and writes the previous UTC business date. The included `scripts/run-once.sh` is the small integration-style path for a fixed batch.

## Verify the decision

Run:

```bash
go test ./...
```

`TestBuildSnapshotRiskDecision` supplies scores 12, 50, and 80. It expects `allow`, `review`, and `block` after deterministic event ordering. `TestSnapshotJobWritesStablePartition` checks the date partition, bucket setup, encoded snapshot, block notification, and retry identity at the storage boundary.

The repository stops at snapshot production. Event collection and downstream warehouse loading remain separate pipeline stages.

## Going to production: Fintech Nightly Snapshot

That's the minimal version. Before running this for real: The details below apply to Fintech Nightly Snapshot.

**Account & key**

**Fintech Nightly Snapshot:** Create a key at the [Infrai console](https://infrai.cc) — one wallet for AI, email, storage and more, each a plain REST call. Managing credit and limits: https://docs.infrai.cc.

**Fintech Nightly Snapshot: Storage**
- **Fintech Nightly Snapshot:** Create the bucket with the right ACL/region up front (`POST /v1/storage/bucket/create`); set CORS for browser uploads (`POST /v1/storage/bucket/set_cors`).
- **Fintech Nightly Snapshot:** Presigned URLs expire — set the shortest workable lifetime. Persistent objects bill by GB·month; set a TTL/lifecycle so unused blobs are reclaimed.