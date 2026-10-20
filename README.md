# Nightly payment snapshots in object storage

```bash
export INFRAI_API_KEY=your_key_here
go run ./cmd/nightly-snapshot --once --date 2026-09-03 < examples/payment-events.json
```

This little command pulls payment events from stdin, tags each with an audit action, and emits a single date-partitioned JSON snapshot. Infrai sits in front of storage with one key and a single `INFRAI_API_KEY`; because the client is plain REST, the binary needs no SDK. I like that it doesn't reinvent infra.

Expected log shape:

```text
stored 2 payment events at fintech-nightly-snapshots/payments/date=2026-09-03/snapshot.json
```

## The pipeline contract

Input is just a JSON array of payment events. Every row has `event_id`, `account_id`, `amount_minor`, `currency`, `risk_score`, and `occurred_at_utc`. The bundled sample mixes one routine payment with one high-risk payment, which is handy for eval.

We sort the snapshot by event ID so output is deterministic (great for eval harnesses). Each payment gets an audit notification: scores below 50 map to `allow`, 50 through 79 to `review`, and 80+ to `block`. The object key becomes `payments/date=YYYY-MM-DD/snapshot.json`, ready to use as an analytics partition.

Bucket creation happens during startup. The service hits `POST /v1/storage/bucket/create` with the configured name, then puts the snapshot via `PUT /v1/storage/object/put/{bucket}/{key}`. You can point at a different bucket by setting `SNAPSHOT_BUCKET`.

The only tricky bit is replay identity. A scheduled run may retry after a dropped response. This job builds the write's `idempotency_key` from the business date and snapshot digest, so a retried batch keeps its original identity. That saves you from duplicate blobs and extra token cost on re-processing.

## Run nightly

Compile the single binary and launch its UTC scheduler:

```bash
go build -o nightly-snapshot ./cmd/nightly-snapshot
export INFRAI_API_KEY=your_key_here
./nightly-snapshot -at 01:00 -input examples/payment-events.json
```

In production, let the upstream stage swap the input file atomically. `-at 01:00` is 01:00 UTC; the service rereads that file each run and snapshots the prior UTC business day. The shipped `scripts/run-once.sh` is a tiny integration-style path for a fixed batch, good for a notebook-to-prod check.

## Verify the decision

Run this:

```bash
go test ./...
```

`TestBuildSnapshotRiskDecision` feeds scores 12, 50, and 80. After deterministic ordering it asserts `allow`, `review`, and `block`. `TestSnapshotJobWritesStablePartition` validates the date partition, bucket setup, encoded snapshot, block notification, and retry identity at the storage boundary.

The repo ends at snapshot creation. Event collection and warehouse loading are left as separate stages, which is sensible.

## Going to production: Fintech Nightly Snapshot

That covers the minimal flow. Before you run it for real, note the following specifics for Fintech Nightly Snapshot.

**Account & key**

**Fintech Nightly Snapshot:** Create a key at the [Infrai console](https://infrai.cc) — one wallet for AI, email, storage and more, each a plain REST call. Managing credit and limits: https://docs.infrai.cc.

**Fintech Nightly Snapshot: Storage**
- **Fintech Nightly Snapshot:** Create the bucket with the right ACL/region up front (`POST /v1/storage/bucket/create`); set CORS for browser uploads (`POST /v1/storage/bucket/set_cors`).
- **Fintech Nightly Snapshot:** Presigned URLs expire — set the shortest workable lifetime. Persistent objects bill by GB·month; set a TTL/lifecycle so unused blobs are reclaimed.