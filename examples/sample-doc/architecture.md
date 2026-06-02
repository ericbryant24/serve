# Export pipeline — architecture

```
┌──────────┐    enqueue    ┌──────────┐   stream   ┌──────────┐
│  client  │ ─────────────▶│  worker  │ ──────────▶│   blob   │
└──────────┘               └──────────┘            └──────────┘
                                │
                                │ notify
                                ▼
                          ┌──────────┐
                          │  email   │
                          └──────────┘
```

The client POSTs an export request, the worker streams rows straight
to blob storage in chunks, and a notification fires when the file is
durable.

## Why a worker pool

Putting export work behind a queue gives us three things the
synchronous path didn't: backpressure for noisy neighbors, retries
on transient blob storage errors, and the ability to drain workers
cleanly during deploys.
