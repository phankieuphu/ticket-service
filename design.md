# Ticket Booking — System Design Detail

> Companion to [`flow.md`](./flow.md). That document states *what* must be true
> (the business invariants). This document details *how* the system is shaped
> to satisfy them — domain model, state machines, sequence flows, and the
> mapping onto this repository's hexagonal layout. Documentation and diagrams
> only; no implementation here.

---

## 1. Domain Model

The sellable unit is a **Ticket**. For seated venues a Ticket wraps exactly one
**Seat**; for general-admission categories `seat_id` is null and the Ticket is
drawn from a counted pool. A **Booking** groups one or more Tickets purchased
together under a single idempotent request.

```mermaid
erDiagram
    EVENT ||--o{ CATEGORY : offers
    CATEGORY ||--o{ TICKET : contains
    CATEGORY ||--|| INVENTORY : "counted by"
    SEAT ||--o| TICKET : "backs (seated only)"
    BOOKING ||--o{ TICKET : claims
    BOOKING ||--o| PAYMENT : "paid by"
    BOOKING ||--o{ AUDIT_LOG : records
    BOOKING ||--o{ OUTBOX_EVENT : emits

    EVENT {
        string event_id PK
        string name
        string venue
        datetime starts_at
        string status
    }
    CATEGORY {
        string category_id PK
        string event_id FK
        string name "e.g. CAT-1"
        money price
    }
    INVENTORY {
        string category_id PK
        int total
        int reserved
        int sold
    }
    SEAT {
        string seat_id PK
        string category_id FK
        string section
        string row
        string number
    }
    TICKET {
        string ticket_id PK
        string category_id FK
        string seat_id FK "nullable, GA has none"
        string booking_id FK "nullable"
        string status
        int version "optimistic lock"
        datetime expires_at
    }
    BOOKING {
        string booking_id PK
        string request_id UK "idempotency key"
        string user_id FK
        string event_id FK
        string status
        money total_amount
        datetime expires_at
    }
    PAYMENT {
        string payment_id PK
        string booking_id FK
        money amount
        string status
        string provider_ref
    }
    OUTBOX_EVENT {
        string outbox_id PK
        string aggregate_id FK
        string type
        string status
    }
    AUDIT_LOG {
        string audit_id PK
        string booking_id FK
        string action
        datetime timestamp
    }
```

**Why a `version` column on `TICKET`:** it backs optimistic locking as a
belt-and-suspenders check alongside the `WHERE status = 'AVAILABLE'` guard
described in §7 of `flow.md` — the write only lands if both the status and the
version the caller read are still current.

**Why `INVENTORY` is separate from counting `TICKET` rows:** a `COUNT(*) …
WHERE status='AVAILABLE'` under millions of rows and concurrent writers is
itself a contention hazard. `INVENTORY` is a small, single-row-per-category
materialized counter that absorbs the first wave of contention cheaply, before
any individual ticket row is touched. See §4.

---

## 2. Ticket State Machine

```mermaid
stateDiagram-v2
    [*] --> AVAILABLE
    AVAILABLE --> RESERVED: claimed by booking
    RESERVED --> PAYMENT_PENDING: payment initiated
    RESERVED --> AVAILABLE: reservation TTL expired
    PAYMENT_PENDING --> CONFIRMED: payment succeeded
    PAYMENT_PENDING --> AVAILABLE: payment failed / timed out
    CONFIRMED --> CANCELLED: explicit cancellation + refund flow
    CANCELLED --> AVAILABLE: released back to inventory
```

Every transition is a single conditional `UPDATE … WHERE status = <expected>`.
An affected-row-count of zero means the caller lost the race and must be
told so — never retried silently into a second sale.

## 3. Booking State Machine

A Booking's status is derived from, but not identical to, its Tickets' state —
a Booking with 3 tickets is `RESERVED` only once *all three* claims succeed;
if any claim fails, the whole booking rolls back (§17 of `flow.md`: "Booked
quantity <= Available quantity" is checked per request, not per ticket).

```mermaid
stateDiagram-v2
    [*] --> PENDING: request validated
    PENDING --> RESERVED: all tickets claimed
    PENDING --> FAILED: any ticket claim failed
    RESERVED --> PAYMENT_PENDING: payment created
    PAYMENT_PENDING --> CONFIRMED: payment succeeded
    PAYMENT_PENDING --> EXPIRED: reservation TTL / payment failed
    RESERVED --> EXPIRED: reservation TTL with no payment attempt
    CONFIRMED --> CANCELLED: refund flow
    FAILED --> [*]
    EXPIRED --> [*]
    CANCELLED --> [*]
```

---

## 4. Reservation Strategy — Two-Phase Atomic Claim

Two independent contention points exist per category, and both must be atomic
on their own, in this order:

**Phase A — Inventory gate (cheap, single row).**
```sql
UPDATE inventory
SET reserved = reserved + :qty
WHERE category_id = :category_id
  AND (total - reserved - sold) >= :qty;
```
Zero affected rows ⇒ reject fast as `SOLD_OUT` without ever touching a ticket
row. This is the row every concurrent request for a hot category contends on
first — see the open question on sharding it in §10.

**Phase B — Ticket claim (specific rows).**
```sql
UPDATE tickets
SET status = 'RESERVED', booking_id = :booking_id, version = version + 1,
    expires_at = :now + :reservation_ttl
WHERE ticket_id IN (
    SELECT ticket_id FROM tickets
    WHERE category_id = :category_id AND status = 'AVAILABLE'
    LIMIT :qty
    FOR UPDATE SKIP LOCKED
);
```
If the claimed row count is less than `:qty` (another transaction raced and
took some rows between the `SELECT` and the lock, or the inventory counter had
drifted), the whole operation rolls back **and** Phase A's reservation is
undone in the same transaction. `SKIP LOCKED` keeps concurrent claimants from
queuing behind each other for rows they were never going to get.

```mermaid
sequenceDiagram
    participant B as Booking Service
    participant DB as Database (tx)
    B->>DB: BEGIN
    B->>DB: Phase A: UPDATE inventory SET reserved += qty WHERE available >= qty
    alt affected = 0
        DB-->>B: 0 rows
        B->>DB: ROLLBACK
        B-->>B: return SOLD_OUT
    else affected = 1
        B->>DB: Phase B: UPDATE tickets ... LIMIT qty FOR UPDATE SKIP LOCKED
        alt claimed < qty
            DB-->>B: partial claim
            B->>DB: ROLLBACK
            B-->>B: return SOLD_OUT
        else claimed = qty
            B->>DB: INSERT booking (status=RESERVED)
            B->>DB: INSERT outbox_event (BookingReserved)
            B->>DB: COMMIT
            B-->>B: return booking (RESERVED)
        end
    end
```

---

## 5. End-to-End Booking Flow (happy path, idempotent)

```mermaid
sequenceDiagram
    actor U as User
    participant GW as API Gateway
    participant R as Redis
    participant BS as Booking Service
    participant DB as Database
    participant K as Kafka (Outbox)

    U->>GW: POST /bookings (Idempotency-Key: abc-123)
    GW->>BS: forward request
    BS->>R: SETNX idem:abc-123 = IN_PROGRESS
    alt key already completed
        R-->>BS: existing booking_id
        BS-->>U: 200 existing booking (no new work)
    else key new
        BS->>DB: two-phase claim (see §4)
        DB-->>BS: booking RESERVED
        BS->>R: SET idem:abc-123 = booking_id
        BS-->>U: 201 booking RESERVED, expires_at
        Note over BS,K: outbox row committed in same tx as booking
        K->>K: publisher polls outbox, publishes BookingReserved
    end
```

Redis holds the idempotency key as a fast path; the database is still the
source of truth (per `flow.md` §12) — a durable idempotency record is written
in the same transaction as the booking, and Redis is a cache in front of it,
not a replacement for it.

## 6. Concurrent Race for the Last Ticket

```mermaid
sequenceDiagram
    participant A as User A
    participant B as User B
    participant DB as Database

    par simultaneous requests
        A->>DB: claim ticket #100 (tx A)
        B->>DB: claim ticket #100 (tx B)
    end
    Note over DB: row lock serializes the two UPDATEs
    DB-->>A: 1 row affected → RESERVED
    DB-->>B: 0 rows affected → already taken
    Note over B: return SOLD_OUT, no retry against the same ticket
```

## 7. Reservation Expiry

```mermaid
sequenceDiagram
    participant S as Expiry Sweeper (scheduled)
    participant DB as Database
    participant K as Kafka (Outbox)

    loop every N seconds
        S->>DB: UPDATE tickets SET status='AVAILABLE' WHERE status='RESERVED' AND expires_at < now()
        S->>DB: UPDATE bookings SET status='EXPIRED' WHERE status IN ('RESERVED','PAYMENT_PENDING') AND expires_at < now()
        S->>DB: UPDATE inventory SET reserved -= qty for each expired booking
        S->>DB: INSERT outbox_event (BookingExpired) per booking
    end
    K->>K: publisher polls outbox → notifies user, releases hold
```

A sweeper (not a per-booking timer) keeps expiry work batchable and avoids one
scheduled job per in-flight reservation at 5M-ticket scale.

## 8. Payment Confirmation / Failure

```mermaid
sequenceDiagram
    actor U as User
    participant BS as Booking Service
    participant PS as Payment Service
    participant DB as Database
    participant K as Kafka (Outbox)

    U->>BS: initiate payment for booking
    BS->>DB: booking.status = PAYMENT_PENDING
    BS->>PS: create payment intent
    PS-->>U: redirect / charge
    PS->>BS: webhook: payment result

    alt payment succeeded
        BS->>DB: tx: booking=CONFIRMED, ticket=CONFIRMED, outbox(BookingConfirmed)
        DB-->>BS: committed
    else payment failed / timed out
        BS->>DB: tx: booking=EXPIRED, ticket=AVAILABLE, inventory.reserved -= qty, outbox(BookingFailed)
        DB-->>BS: committed
    end
    K->>K: publisher polls outbox → email / notification / analytics
```

The booking transaction never waits on the notification/email/analytics
consumers — it commits against the outbox row and returns; delivery to
downstream services happens asynchronously off that row (`flow.md` §13).

---

## 9. Component Mapping (this repository)

Hexagonal layering already scaffolded in the repo, and where each design
element above lives:

```mermaid
flowchart TB
    subgraph adapters_in["Inbound Adapters"]
        HTTP["internal/adapters/http\n(gin server, dto)"]
    end

    subgraph domain["Domain"]
        ENT["internal/domain/entity\nBooking · Ticket · Seat"]
        PORTSVC["internal/domain/ports/services.go\nuse-case interfaces"]
        PORTREPO["internal/domain/ports/repository.go\npersistence interfaces"]
        SVC["internal/domain/services\nbooking.go — orchestrates\nclaim / confirm / expire"]
    end

    subgraph adapters_out["Outbound Adapters"]
        DBP["internal/adapters/database/provider\nmysql.go / dynamodb.go"]
        CACHE["internal/adapters/cache\nredis.go — idempotency, rate limit"]
        KAFKA["internal/adapters/kafka\nproducer.go / consumer.go — outbox publish"]
    end

    HTTP --> PORTSVC
    PORTSVC --> SVC
    SVC --> ENT
    SVC --> PORTREPO
    SVC --> CACHE
    PORTREPO --> DBP
    SVC --> KAFKA
```

`domain/services/booking.go` is the only place the two-phase claim (§4) and
the state machines (§2, §3) are actually enforced — adapters stay thin
translators in and out of the domain.

---

## 10. API Contract (design-level)

| Method & Path | Purpose | Idempotent via |
|---|---|---|
| `POST /v1/events/{event_id}/bookings` | Reserve tickets | `Idempotency-Key` header → `request_id` |
| `GET /v1/bookings/{booking_id}` | Poll booking status | — (read) |
| `POST /v1/bookings/{booking_id}/payment` | Start payment | Payment provider idempotency key |
| `POST /v1/payments/webhook` | Provider payment callback | Provider event id, deduped |
| `POST /v1/bookings/{booking_id}/cancel` | Cancel a confirmed booking | — (explicit action, not retried) |
| `GET /v1/events/{event_id}/availability` | Read current per-category availability | — (read, cache-fronted) |

Price and availability are always re-read server-side from `CATEGORY` /
`INVENTORY` — a request body never carries a trusted price or ticket status
(`flow.md` §15).

---

## 11. Open Design Questions

These are flagged, not resolved, here:

- **Inventory row contention at 5M-ticket scale.** A single `INVENTORY` row
  per category is still one hot row at sale-opening instant. Options: shard
  the counter (`category_id + shard_no`) and sum on read, or front it with a
  Redis atomic `DECRBY` Lua script reconciled against the DB asynchronously.
  Needs a load-test-driven decision, not an assumed answer.
- **Seat vs. general-admission duality.** `SEAT` is optional on `TICKET`
  today; confirm whether every category is seated for this event or whether a
  GA pool (no `SEAT` rows at all, tickets pre-materialized or virtual) is
  actually required.
- **Distributed lock necessity.** §10–11 of `flow.md` rule out relying on
  `sync.Mutex`; the design above relies on DB row locks alone (no Redis
  Redlock). Confirm this is sufficient before introducing a second
  consistency mechanism to reason about.
- **Outbox publisher delivery semantics.** At-least-once with consumer-side
  dedup is assumed; confirm downstream consumers (notification, analytics)
  are built to be idempotent on `outbox_id`.
