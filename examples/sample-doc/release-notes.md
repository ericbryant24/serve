# Q3 Release Notes — draft

We shipped the new export pipeline this quarter. Here's what's
landing, what's still rolling out, and what we cut.

## What's new

**Async exports.** Long-running exports no longer block the request
thread. Users get a job ID and a download link by email when the
file is ready.

**CSV and Parquet in one path.** Both formats run through the same
serializer, which means schema changes only need to be made once.

**Per-org quotas.** Each org gets a configurable monthly limit; the
default is 50GB. Exceeding the limit returns a 429 with a retry
window in the response body.

## Performance

The new pipeline is roughly 4× faster than the legacy path for
exports over 1M rows. Smaller exports see a more modest 1.5–2×
improvement, since the constant-factor overhead dominates.

```python
# Triggering an async export
job = client.exports.create(
    format="parquet",
    query="SELECT * FROM events WHERE ts > NOW() - INTERVAL '7 days'",
    notify_email="ops@example.com",
)
print(job.id)  # exp_4d8a91…
```

## Rollout

| Cohort        | Status       | Date      |
|---------------|--------------|-----------|
| Internal      | Live         | Aug 14    |
| Beta orgs     | Live         | Sep 02    |
| GA            | Rolling out  | Sep 28    |

We're holding 5% of GA traffic on the legacy path through October
as a fallback while we monitor error rates.

## What we cut

We dropped the planned **streaming JSON** export. The throughput
gains over Parquet were marginal and the implementation surface
was much larger than expected. We'll revisit if customers ask for
it explicitly.
