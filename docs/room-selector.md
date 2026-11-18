# Campus room selector

Approved scope: add `balanced` and `fulfill` campus policies and a date-specific
recommendation on the client campus-room list. `date=YYYY-MM-DD` is required;
past dates are rejected. Admin listing remains unchanged. No booking reservation
or automatic ticket reassignment is introduced.

The campus owns `selection_policy`; the model/database default is `balanced`,
including existing campuses migrated at startup. Campus CRUD validates policies.
The room service selects enabled rooms with a service date and remaining capacity,
using the booking status exclusions. Balanced orders by ticket count then room ID;
fulfill orders by room ID until capacity is exhausted. Name/type filters apply to
all candidates independently of pagination. No eligible room returns null.

Verification completed:

- `go test ./...` passed with a writable cache (`GOCACHE=/tmp/clinic-go-cache`).
- `go test -race ./...` and `go vet ./...` passed with the same cache.
- Behavior coverage includes policy persistence/validation, both policy examples,
  ties, full/zero-capacity rooms, disabled/closed rooms, campus/date isolation,
  booking status exclusions, filters, cancellation and database failures.
- Client tests cover required/invalid/past dates, off-page recommendations, null
  results, and unchanged admin listing. Existing-campus AutoMigrate is tested twice.
- A separate reviewer inspected the implementation and added verification tests;
  no blocking or remaining concrete findings were reported.

No design deviations. Selection remains advisory: concurrent bookings can change
availability before ticket insertion. Verification uses SQLite; no production
server operations or PostgreSQL integration checks were performed.
