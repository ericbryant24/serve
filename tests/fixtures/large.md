---
title: "Distributed Systems Design Guide"
author: "Engineering Team"
version: "3.2.1"
---

# Distributed Systems Design Guide

This guide covers the core principles, patterns, and trade-offs involved in designing distributed systems. It is intended for engineers building services that span multiple machines, datacenters, or cloud regions.

## Table of Contents

1. [Fundamentals](#fundamentals)
2. [Consistency Models](#consistency-models)
3. [Consensus Algorithms](#consensus-algorithms)
4. [Replication Strategies](#replication-strategies)
5. [Partition Tolerance](#partition-tolerance)
6. [Storage Engines](#storage-engines)
7. [Networking Patterns](#networking-patterns)
8. [Observability](#observability)
9. [Failure Modes](#failure-modes)
10. [Case Studies](#case-studies)

---

## Fundamentals

### The CAP Theorem

The CAP theorem states that a distributed system can guarantee at most two of three properties simultaneously:

- **Consistency**: Every read receives the most recent write or an error
- **Availability**: Every request receives a response (not necessarily the most recent)
- **Partition tolerance**: The system continues operating despite network partitions

In practice, network partitions are unavoidable, so the real choice is between CP and AP systems.

| System Type | Examples | Tradeoff |
|-------------|----------|----------|
| CP | HBase, Zookeeper, etcd | Sacrifices availability during partitions |
| AP | Cassandra, CouchDB, Riak | Sacrifices consistency for availability |
| CA | Single-node RDBMS | Only feasible without partitions |

### Fallacies of Distributed Computing

Peter Deutsch's eight fallacies remind us that distributed systems introduce assumptions that fail in production:

1. The network is reliable
2. Latency is zero
3. Bandwidth is infinite
4. The network is secure
5. Topology doesn't change
6. There is one administrator
7. Transport cost is zero
8. The network is homogeneous

Each fallacy corresponds to a class of bugs that only surface under load or during infrastructure events.

---

## Consistency Models

### Strong Consistency

Under strong consistency, all reads reflect the most recent write. This requires coordination between nodes on every write, which limits throughput and increases latency.

```python
class StrongConsistentStore:
    def __init__(self, replicas: list[Node]):
        self.replicas = replicas
        self.quorum = len(replicas) // 2 + 1

    def write(self, key: str, value: bytes) -> bool:
        acks = 0
        for replica in self.replicas:
            if replica.write(key, value):
                acks += 1
        return acks >= self.quorum

    def read(self, key: str) -> bytes | None:
        responses = [r.read(key) for r in self.replicas]
        # Return value only if quorum agrees
        return self._quorum_value(responses)
```

### Eventual Consistency

Eventual consistency guarantees that if no new updates are made, all replicas will converge to the same value. This allows much higher write throughput at the cost of stale reads.

```go
type EventualStore struct {
    localData map[string]VersionedValue
    peers     []Peer
    mu        sync.RWMutex
}

func (s *EventualStore) Write(key string, val []byte) {
    s.mu.Lock()
    s.localData[key] = VersionedValue{
        Value:   val,
        Version: time.Now().UnixNano(),
        NodeID:  s.nodeID,
    }
    s.mu.Unlock()
    go s.gossip(key) // async replication
}
```

### Read-Your-Writes

Read-your-writes consistency ensures that after a client writes a value, subsequent reads from that client will see the written value. This is typically implemented via sticky sessions or version tokens.

### Monotonic Reads

Monotonic reads guarantee that once a client has seen a value at version V, it will never see an older version. Implemented by tracking the minimum version a client has observed.

---

## Consensus Algorithms

### Paxos

Paxos is a family of protocols for achieving consensus in a network of unreliable nodes. The basic single-decree Paxos operates in two phases:

**Phase 1 (Prepare/Promise)**
A proposer sends a `Prepare(n)` message to acceptors. Each acceptor promises not to accept proposals numbered less than `n` and returns the highest-numbered proposal it has accepted.

**Phase 2 (Accept/Accepted)**
If the proposer receives promises from a majority, it sends `Accept(n, v)` where `v` is the value from the highest-numbered prior proposal, or its own value if no prior proposals exist.

```
Proposer                 Acceptor A    Acceptor B    Acceptor C
   |--Prepare(5)-------->|             |             |
   |--Prepare(5)-------->|             |             |
   |--Prepare(5)------------------------------>|
   |<-Promise(5, nil)----|             |             |
   |<-Promise(5, nil)----|             |             |
   |<-Promise(5, nil)-------------------------|
   |--Accept(5, "v")---->|             |             |
   |--Accept(5, "v")-----|             |             |
   |--Accept(5, "v")------------------------->|
   |<-Accepted(5)--------|             |             |
```

### Raft

Raft was designed to be more understandable than Paxos. It decomposes consensus into three sub-problems:

1. **Leader election**: Nodes start as followers. If no heartbeat arrives within an election timeout, a follower becomes a candidate and requests votes.
2. **Log replication**: The leader accepts entries from clients and replicates them to followers.
3. **Safety**: Only nodes with up-to-date logs can be elected leader.

```typescript
interface RaftNode {
  state: "follower" | "candidate" | "leader";
  currentTerm: number;
  votedFor: string | null;
  log: LogEntry[];
  commitIndex: number;
  lastApplied: number;
}

function handleRequestVote(
  node: RaftNode,
  req: RequestVoteRequest
): RequestVoteResponse {
  if (req.term < node.currentTerm) {
    return { term: node.currentTerm, voteGranted: false };
  }
  const logOk =
    req.lastLogTerm > lastLogTerm(node) ||
    (req.lastLogTerm === lastLogTerm(node) &&
      req.lastLogIndex >= node.log.length - 1);
  const voteGranted =
    logOk && (node.votedFor === null || node.votedFor === req.candidateId);
  return { term: node.currentTerm, voteGranted };
}
```

---

## Replication Strategies

### Single-Leader Replication

All writes go to one leader node, which replicates to followers. Reads can be served from any replica (with potential staleness) or from the leader (with guaranteed freshness).

**Advantages:**
- Simple to reason about
- No write conflicts
- Easy to implement transactions

**Disadvantages:**
- Leader is a bottleneck
- Failover requires electing a new leader
- Followers may lag during high write load

### Multi-Leader Replication

Multiple nodes accept writes and replicate to each other. Common in multi-datacenter deployments where cross-DC write latency would be prohibitive.

**Conflict resolution strategies:**

| Strategy | Description | Use Case |
|----------|-------------|----------|
| Last-Write-Wins | Keep the write with the highest timestamp | Append-only data |
| Merge | Application-specific merge function | Shopping carts, CRDT |
| Custom resolution | Expose conflict to application | Documents |
| Avoid conflicts | Route writes for same key to same leader | User settings |

### Leaderless Replication (Dynamo-style)

Any node can accept writes. The client writes to W nodes and reads from R nodes. As long as W + R > N (total replicas), reads will see the latest write.

```python
def write(key: str, value: bytes, w: int = 2, n: int = 3) -> bool:
    nodes = consistent_hash(key, n)
    acks = sum(1 for node in nodes if node.write(key, value))
    return acks >= w

def read(key: str, r: int = 2, n: int = 3) -> bytes:
    nodes = consistent_hash(key, n)
    responses = [node.read(key) for node in nodes[:r]]
    return max(responses, key=lambda x: x.version).value
```

---

## Partition Tolerance

### Detecting Partitions

Nodes detect partitions via heartbeat timeouts. False positives (slow network mistaken for failure) are a significant operational hazard.

**Phi accrual failure detector** computes a suspicion level φ based on inter-arrival times:

```
φ(t) = -log₁₀(Prob(interval > t))
```

When φ exceeds a threshold (typically 8–10), the node is considered failed.

### Handling Split-Brain

In a two-node cluster, a network partition creates two independent nodes that both believe the other has failed. Without a tiebreaker, both may accept writes, leading to divergent state.

Solutions:
- **Fencing tokens**: A monotonically increasing token is issued with each lease. Storage layer rejects requests with stale tokens.
- **STONITH** (Shoot The Other Node In The Head): One partition forcibly reboots the other via out-of-band control plane.
- **Witness/quorum**: A third node (or object storage) acts as a quorum witness.

---

## Storage Engines

### Log-Structured Merge Trees (LSM)

LSM trees write to an in-memory buffer (memtable), periodically flushing sorted runs (SSTables) to disk. Background compaction merges and garbage-collects SSTables.

```
Write path:
  Client write → WAL (durability) → Memtable (fast write)
                                         ↓ (when full)
                                      SSTable L0
                                         ↓ (compaction)
                                      SSTable L1, L2, ...

Read path:
  Client read → Memtable → Bloom filter → SSTable binary search
```

**Used by:** RocksDB, LevelDB, Cassandra, HBase

### B-Trees

B-trees update data in-place on disk pages (typically 4–16 KB). Writes modify pages directly, protected by a write-ahead log.

| Property | LSM Tree | B-Tree |
|----------|----------|--------|
| Write amplification | Low (sequential) | High (random) |
| Read amplification | Higher (multiple SSTables) | Lower (single path) |
| Space amplification | Higher (stale data until compaction) | Lower |
| Best for | Write-heavy | Read-heavy |

### MVCC (Multi-Version Concurrency Control)

MVCC maintains multiple versions of each row, allowing readers to see a snapshot without blocking writers. Each row has a `txid_min` and `txid_max` indicating which transactions can see it.

```sql
-- PostgreSQL MVCC: each row has hidden system columns
SELECT xmin, xmax, ctid, * FROM orders WHERE id = 42;

--  xmin  | xmax | ctid  | id |  total
-- -------+------+-------+----+--------
--  15234 |    0 | (0,1) | 42 | 199.99
```

---

## Networking Patterns

### Service Mesh

A service mesh intercepts all inter-service traffic via sidecar proxies (e.g., Envoy). This offloads concerns like mTLS, circuit breaking, retries, and observability from application code.

```yaml
apiVersion: networking.istio.io/v1alpha3
kind: DestinationRule
metadata:
  name: orders-circuit-breaker
spec:
  host: orders-service
  trafficPolicy:
    connectionPool:
      tcp:
        maxConnections: 100
      http:
        http1MaxPendingRequests: 50
        maxRequestsPerConnection: 10
    outlierDetection:
      consecutive5xxErrors: 5
      interval: 10s
      baseEjectionTime: 30s
```

### Backpressure

Backpressure prevents fast producers from overwhelming slow consumers. In reactive streams, a subscriber signals how many items it can handle via `request(n)`.

```go
func processOrders(in <-chan Order, out chan<- Result, maxInFlight int) {
    sem := make(chan struct{}, maxInFlight)
    for order := range in {
        sem <- struct{}{}  // acquire
        go func(o Order) {
            defer func() { <-sem }()  // release
            out <- process(o)
        }(order)
    }
}
```

### gRPC Streaming

gRPC supports four communication patterns:

| Pattern | Description |
|---------|-------------|
| Unary | Single request, single response |
| Server streaming | Single request, stream of responses |
| Client streaming | Stream of requests, single response |
| Bidirectional | Stream of requests, stream of responses |

---

## Observability

### The Three Pillars

**Metrics**: Aggregated numerical measurements over time. Low cardinality. High compression. Best for alerting.

**Logs**: Discrete events with structured context. High cardinality. Used for debugging specific incidents.

**Traces**: Causal chains of operations across services. Links requests end-to-end. Essential for latency analysis.

### RED Method

For each service, track:
- **Rate**: Requests per second
- **Errors**: Error rate (%)
- **Duration**: Latency distribution (p50, p95, p99)

```promql
# Error rate over 5 minutes
rate(http_requests_total{status=~"5.."}[5m])
  /
rate(http_requests_total[5m])

# p99 latency
histogram_quantile(0.99,
  rate(http_request_duration_seconds_bucket[5m])
)
```

### Structured Logging

```go
log.Info("request completed",
    slog.String("method", r.Method),
    slog.String("path", r.URL.Path),
    slog.Int("status", w.status),
    slog.Duration("duration", time.Since(start)),
    slog.String("trace_id", traceID),
)
```

---

## Failure Modes

### Cascading Failures

A cascading failure occurs when the failure of one component increases load on others, causing them to fail in turn. Common triggers:

1. A slow dependency causes request queues to back up
2. Retries amplify load on the already-struggling service
3. Circuit breakers open too late or not at all

**Mitigation**: Circuit breakers, bulkheads, load shedding, timeout budgets.

### Byzantine Failures

A Byzantine failure is when a node behaves arbitrarily (sends different values to different peers, lies about its state). Tolerating f Byzantine failures requires at least 3f+1 nodes.

Practical Byzantine Fault Tolerance (PBFT) has O(n²) message complexity — too expensive for large clusters. Modern systems use BFT only when nodes are operated by mutually distrusting parties (blockchains).

### Herd Effect

When a resource becomes available after an outage, many clients retry simultaneously, overwhelming the service. Mitigated by:

- Exponential backoff with jitter
- Thundering-herd protection in load balancers
- Request hedging (send duplicate requests after a short delay, cancel the slower one)

```python
def retry_with_jitter(fn, max_attempts=5, base_delay=0.1):
    for attempt in range(max_attempts):
        try:
            return fn()
        except TransientError:
            delay = base_delay * (2 ** attempt)
            jitter = random.uniform(0, delay * 0.1)
            time.sleep(delay + jitter)
    raise MaxRetriesExceeded()
```

---

## Case Studies

### Google Spanner

Spanner is Google's globally distributed, externally consistent database. Key innovations:

- **TrueTime API**: GPS and atomic clocks provide a bounded uncertainty interval for wall-clock time. Commits wait out the uncertainty interval to guarantee external consistency.
- **Paxos groups**: Each shard is managed by a Paxos group. Leaders hold 10-second leases, renewable before expiry.
- **2PC across groups**: Cross-shard transactions use two-phase commit, with Paxos groups as participants.

### Amazon DynamoDB

DynamoDB's design principles prioritize availability and predictable latency:

- Consistent hashing with virtual nodes for balanced data distribution
- Sloppy quorum: writes go to W of the N "preferred" nodes; if some are unavailable, "hinted handoff" stores writes on temporary nodes
- Anti-entropy via Merkle trees to detect divergence between replicas
- Gossip protocol for membership and failure detection

### Kafka Log Architecture

Kafka models a topic as an append-only, partitioned log:

```
Topic: orders
  Partition 0: [msg0] [msg1] [msg2] [msg3] ...
  Partition 1: [msg0] [msg1] [msg2] ...
  Partition 2: [msg0] [msg1] ...

Consumer group A:
  Consumer 1 → Partition 0 (offset: 142)
  Consumer 2 → Partition 1 (offset: 89)
  Consumer 3 → Partition 2 (offset: 201)
```

Producers write to the leader. Followers replicate asynchronously. ISR (In-Sync Replicas) must acknowledge before the leader returns success (when `acks=all`).

---

## Summary

Distributed systems involve fundamental trade-offs with no universally correct answer:

| Decision | Option A | Option B |
|----------|----------|----------|
| Consistency | Strong (CP) | Eventual (AP) |
| Replication | Single-leader | Multi-leader |
| Storage | LSM (write-optimized) | B-tree (read-optimized) |
| Consensus | Raft (simpler) | Paxos (more flexible) |
| Failure detection | Heartbeat timeout | Phi accrual |

The right choice depends on your workload characteristics, team's operational experience, and tolerance for complexity. Benchmark under realistic conditions before committing to an architecture.

---

*Last updated: 2026-05-11*
