# Ticket Booking Business Flow

## 1. Purpose

This document describes the business flow and system requirements for a high-concurrency ticket booking system.

The system is designed for a large-scale event such as the **FIFA World Cup 2026**, where millions of tickets may become available for sale at the same time.

For example:

* Total available tickets: **5 million**
* Millions of users may attempt to purchase tickets simultaneously.
* A single ticket must never be sold to more than one user.
* The system must not sell more tickets than the actual inventory.
* The system must remain consistent even when multiple requests target the same ticket at the same time.

The primary goals are:

1. **Strong inventory consistency**
2. **No race conditions**
3. **No overselling**
4. **No lost bookings**
5. **Fair and predictable booking behavior**
6. **Idempotent requests**
7. **Secure user and payment handling**
8. **High availability and scalability**

---

# 2. Business Scenario

Assume there are **5 million tickets** available for a World Cup match.

At the ticket-sale opening time, a very large number of users may send booking requests simultaneously.

For example:

```text
09:00:00

User A ──────┐
User B ──────┤
User C ──────┤
User D ──────┤──> Booking Service
User E ──────┤
User F ──────┤
...          │
Millions ────┘
```

The system must process these requests concurrently while maintaining the correctness of ticket inventory.

The most important business rule is:

> **One ticket can belong to only one successful booking.**

---

# 3. Core Requirements

## 3.1 No Overselling

The system must never sell more tickets than are actually available.

For example:

```text
Available tickets = 100

Requests = 1,000

Successful bookings <= 100
```

It must never produce:

```text
Successful bookings = 101
```

even if hundreds or thousands of requests are processed concurrently.

---

## 3.2 No Double Booking

Two users must never successfully purchase the same ticket.

Example:

```text
Ticket #1001

User A ──> Booking request
User B ──> Booking request
```

Only one request can successfully reserve:

```text
Ticket #1001 -> User A
```

The other request must receive a failure response such as:

```text
Ticket is no longer available.
```

---

## 3.3 Prevent Race Conditions

A race condition can occur when multiple requests read and modify the same inventory simultaneously.

Example:

```text
Initial inventory = 1

User A:
    READ inventory = 1

User B:
    READ inventory = 1

User A:
    inventory = 0

User B:
    inventory = 0
```

Both users may incorrectly believe they successfully purchased the last ticket.

The system must make the inventory operation **atomic**.

Conceptually:

```text
IF available > 0
THEN decrease available by 1
AND create reservation
ELSE reject request
```

This operation must behave as one consistent operation.

---

# 4. Ticket State

A ticket should have a clearly defined lifecycle.

For example:

```text
AVAILABLE
    |
    v
RESERVED
    |
    v
PAYMENT_PENDING
    |
    v
CONFIRMED
```

Failure or timeout may cause:

```text
RESERVED
    |
    v
EXPIRED
    |
    v
AVAILABLE
```

A possible state machine:

```text
                    ┌──────────────┐
                    │   AVAILABLE  │
                    └──────┬───────┘
                           │
                           │ Reserve
                           ▼
                    ┌──────────────┐
                    │   RESERVED   │
                    └──────┬───────┘
                           │
                    Payment successful
                           │
                           ▼
                    ┌──────────────┐
                    │  CONFIRMED   │
                    └──────────────┘

                    RESERVED
                       │
                 Timeout / failure
                       │
                       ▼
                  AVAILABLE
```

Invalid state transitions must be rejected.

For example:

```text
CONFIRMED -> AVAILABLE
```

should not happen directly unless there is an explicit cancellation/refund business flow.

---

# 5. Booking Request

Each booking request should contain a unique request identifier.

Example:

```json
{
    "request_id": "req_123456",
    "user_id": "user_123",
    "event_id": "worldcup_2026_match_001",
    "ticket_id": "ticket_1001",
    "quantity": 2
}
```

The `request_id` is important for **idempotency**.

If the client sends the same request multiple times because of a network timeout:

```text
Request #1
Request #1 retry
Request #1 retry
```

the system should not create multiple bookings.

Instead:

```text
request_id = req_123456

First request:
    Booking created

Retry:
    Return existing booking
```

---

# 6. Idempotency Requirement

The booking API must be idempotent.

For example:

```http
POST /bookings
Idempotency-Key: abc-123
```

If the same key is received multiple times:

```text
abc-123
abc-123
abc-123
```

the system should process it only once.

Expected behavior:

```text
First request
    ↓
Create booking
    ↓
Return booking_id = booking_001

Retry
    ↓
Find abc-123
    ↓
Return booking_001
```

This prevents duplicate bookings caused by:

* Network retries
* Client retries
* Load balancer retries
* Mobile application retries
* User double-clicking the purchase button

---

# 7. Inventory Consistency

Inventory is the most critical part of the system.

For example:

```text
Ticket inventory:

Event: World Cup 2026
Category: CAT-1

Total:     5,000,000
Reserved:  1,000,000
Available: 4,000,000
```

The system must maintain:

```text
Available = Total - Reserved - Sold
```

The inventory update must be atomic.

A dangerous implementation would be:

```text
SELECT available
FROM inventory

// Application calculates

available = available - quantity

UPDATE inventory
SET available = available
```

Under high concurrency, this can cause lost updates.

Instead, the system should use an atomic operation or appropriate database locking/transaction mechanism.

Conceptually:

```sql
UPDATE inventory
SET available = available - ?
WHERE event_id = ?
  AND available >= ?;
```

The application then checks the affected row count.

```text
affected rows = 1
    -> reservation successful

affected rows = 0
    -> insufficient inventory
```

This prevents the inventory from becoming negative.

---

# 8. Transaction Consistency

Booking and inventory changes must be consistent.

For example:

```text
Inventory:
    available = 1

Booking:
    ticket = RESERVED
```

We must avoid this situation:

```text
Inventory decreased
    ↓
Booking creation failed
```

because the ticket would effectively disappear.

Similarly, we must avoid:

```text
Booking created
    ↓
Inventory update failed
```

because the system could oversell the ticket.

Therefore, operations that must be atomic should be protected by an appropriate transaction/consistency mechanism.

Example:

```text
BEGIN TRANSACTION

1. Validate request
2. Validate ticket availability
3. Reserve inventory
4. Create booking
5. Create booking event/outbox record

COMMIT
```

If a critical operation fails:

```text
ROLLBACK
```

---

# 9. Payment Flow

Payment should not be treated as the same operation as the initial inventory reservation.

A recommended flow is:

```text
User
  |
  | Book ticket
  v
Booking Service
  |
  | Reserve inventory
  v
Ticket = RESERVED
  |
  | Create payment
  v
Payment Service
  |
  | Payment successful
  v
Booking = CONFIRMED
```

If payment is not completed within the reservation timeout:

```text
RESERVED
    |
    | Timeout
    v
EXPIRED
    |
    v
Inventory released
    |
    v
AVAILABLE
```

For example:

```text
Reservation timeout = 10 minutes
```

The exact timeout is a business decision.

---

# 10. Concurrency

The system must support many concurrent booking requests.

Example:

```text
                 ┌── User A
                 ├── User B
                 ├── User C
Millions ────────┼── User D
                 ├── User E
                 └── ...
                       |
                       v
                Booking Service
                       |
              ┌────────┴────────┐
              │                 │
          Inventory          Booking
              │                 │
              └────────┬────────┘
                       v
                    Database
```

The application must not rely only on an in-memory lock such as:

```go
sync.Mutex
```

for distributed booking consistency.

A mutex only protects memory inside one application process.

If there are multiple instances:

```text
Booking Service #1
Booking Service #2
Booking Service #3
Booking Service #4
```

each instance has its own mutex.

Therefore:

```text
Request A -> Instance #1 -> Mutex #1
Request B -> Instance #2 -> Mutex #2
```

These requests can still modify the same ticket concurrently.

Distributed consistency must therefore be handled using mechanisms shared by all application instances, such as database atomic operations/transactions and, where appropriate, distributed coordination.

---

# 11. Distributed Architecture

The system should be horizontally scalable.

Example:

```text
                    Load Balancer
                         |
          ┌──────────────┼──────────────┐
          │              │              │
          ▼              ▼              ▼
     Booking #1     Booking #2     Booking #3
          │              │              │
          └──────────────┼──────────────┘
                         |
                    Database
```

More instances can be added when traffic increases.

However, adding more application instances must not weaken consistency.

The database remains the authoritative source for ticket ownership and inventory.

---

# 12. Redis

Redis can be used to reduce load and improve performance, but it should not blindly become the only source of truth for ticket ownership.

Possible Redis use cases:

* Distributed locking
* Idempotency keys
* Rate limiting
* Short-lived reservation data
* Caching
* Request throttling

For example:

```text
User
  |
  v
API
  |
  +--> Redis
  |     |
  |     +--> rate limit
  |     +--> idempotency
  |
  v
Database
      |
      +--> authoritative ticket state
```

The system should clearly define which component is authoritative.

For ticket ownership:

```text
Database = source of truth
```

Redis should not cause a ticket to be considered sold if the durable transaction did not succeed.

---

# 13. Kafka / Message Queue

Asynchronous messaging can be used for non-critical downstream operations.

For example:

```text
Booking Transaction
       |
       v
Create Booking + Outbox Event
       |
       v
Message Broker
       |
       ├── Notification Service
       ├── Email Service
       ├── Analytics
       ├── Reporting
       └── Audit Service
```

The critical booking transaction should not depend on an email service being available.

For example:

```text
Booking successful
    ↓
Outbox event created
    ↓
Transaction committed
```

Then:

```text
Outbox Publisher
    ↓
Kafka
    ↓
Email / Notification / Analytics
```

This prevents a temporary failure in downstream services from causing a successful booking to be lost.

---

# 14. Failure Handling

The system must handle failures such as:

### Database failure

If the database transaction fails:

```text
Booking = NOT CREATED
Inventory = NOT CHANGED
```

The client should receive an appropriate retryable error.

### Payment failure

```text
Booking = RESERVED
Payment = FAILED
       |
       v
Release reservation
       |
       v
Ticket = AVAILABLE
```

### Client timeout

The client may not know whether the booking succeeded.

Example:

```text
Client -> Booking API

Booking API -> Database
Database -> Booking API

X Network timeout

Client does not receive response
```

The client retries using the same idempotency key.

The server should return the existing booking instead of creating another one.

---

# 15. Security Requirements

The booking system must also protect against malicious or abusive traffic.

Requirements include:

* Authentication
* Authorization
* Rate limiting
* Request validation
* Idempotency protection
* Input validation
* API throttling
* Audit logging
* Protection against replayed requests
* Secure payment integration
* Do not trust client-provided ticket prices
* Do not trust client-provided ticket ownership
* Server-side validation of ticket availability

For example, the client should never be able to send:

```json
{
    "price": 10
}
```

and have the backend trust that price.

The backend should retrieve the actual price from the authoritative pricing configuration.

---

# 16. Auditability

Every important booking operation should be auditable.

For example:

```text
Booking Created
Booking Reserved
Payment Started
Payment Successful
Booking Confirmed
Booking Expired
Booking Cancelled
Ticket Released
```

An audit record could contain:

```text
event_id
booking_id
user_id
ticket_id
action
timestamp
request_id
actor
metadata
```

This is important for:

* Customer support
* Financial reconciliation
* Fraud investigation
* Dispute handling
* Regulatory requirements
* Debugging production incidents

---

# 17. Business Invariants

The following invariants must always hold.

### Inventory

```text
available >= 0
```

### Ticket ownership

```text
One ticket -> Maximum one active owner
```

### Booking

```text
One successful booking request
    -> One booking
```

### Payment

```text
One confirmed booking
    -> Valid successful payment
```

### Quantity

```text
Booked quantity <= Available quantity
```

These invariants are more important than simply achieving high throughput.

---

# 18. Expected High-Concurrency Scenario

Assume:

```text
Available tickets = 1

User A -> Ticket #100
User B -> Ticket #100
User C -> Ticket #100
```

All requests arrive at almost exactly the same time.

The expected result is:

```text
User A -> SUCCESS
User B -> SOLD OUT
User C -> SOLD OUT
```

Never:

```text
User A -> SUCCESS
User B -> SUCCESS
```

and never:

```text
Inventory = -1
```

---

# 19. Success Criteria

The booking system is considered correct when:

* No ticket can be sold twice.
* Inventory can never become negative.
* Successful bookings never exceed available inventory.
* Duplicate requests do not create duplicate bookings.
* Booking state transitions are valid.
* Failed transactions do not consume inventory permanently.
* Expired reservations release inventory correctly.
* Payment failures do not result in confirmed bookings.
* The system can scale horizontally.
* Multiple application instances can safely process the same ticket concurrently.
* Critical booking data remains durable.
* Downstream service failures do not cause successful bookings to disappear.
* All important booking operations can be audited.

---

# 20. Main Principle

The most important principle of the system is:

> **Optimize for correctness first, then optimize for throughput.**

For a ticketing system, selling the same ticket twice is much worse than temporarily rejecting or delaying a request.

The system should therefore guarantee:

```text
Consistency
     +
Atomicity
     +
Idempotency
     +
Concurrency Control
     +
Durability
     +
Security
```

before optimizing for maximum request throughput.

A simplified high-level flow is:

```text
                 ┌──────────────┐
                 │     User     │
                 └──────┬───────┘
                        │
                        ▼
                 ┌──────────────┐
                 │ API Gateway  │
                 └──────┬───────┘
                        │
                        ▼
                 ┌──────────────┐
                 │    Booking   │
                 │   Service    │
                 └──────┬───────┘
                        │
              ┌─────────┼─────────┐
              │         │         │
              ▼         ▼         ▼
           Redis    Database   Payment
              │         │         │
              │         │         │
              │    Transaction    │
              │         │         │
              │         ▼         │
              │      Booking      │
              │         │         │
              │         ▼         │
              │      Outbox       │
              │         │         │
              │         ▼         │
              │       Kafka       │
              │         │         │
              │    ┌────┴────┐    │
              │    ▼         ▼    │
              │ Notification Audit
              │
              └───────────────────
```
