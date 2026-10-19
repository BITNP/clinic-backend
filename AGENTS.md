# AGENTS.md

## Project Overview

This is a Go backend project.

The AI agent should write simple, idiomatic Go code. Prefer clear control flow, explicit error handling, small functions, and standard library solutions unless a dependency already exists in the project.

## Basic Rules

* Follow the module organization rules below when adding or moving files.
* Do not introduce new frameworks or large dependencies unless explicitly asked.
* Prefer small, focused changes over large rewrites.
* Keep public APIs stable unless the task explicitly requires changing them.
* Do not silently ignore errors.
* Do not use `panic` for normal application errors.
* Do not add unnecessary abstraction, generic helpers, or “enterprise-style” layers.
* Avoid global mutable state unless the existing project already uses it.
* Keep concurrency simple and safe. Use goroutines only when they clearly help.
* Use `context.Context` for request-scoped cancellation, deadlines, and values.

## Project Organization

Organize production code by business module under `internal/`. Before adding a
file, identify the module that owns the behavior and inspect its existing packages.
Do not recreate global `handlers/`, `services/`, or `models/` directories.

* Modules include `auth`, `record`, `scheduling`, `room`, `staff`,
  `announcement`, `tagging`, `changes`, and `legacy`.
* Customer tickets and staff/admin repair operations belong to `record`; service
  dates and work schedules belong to `scheduling`. Organize by business ownership,
  not by API audience or one module per database table.
* Small modules may keep their files at the module root. When a category grows
  beyond three production Go files, put **all** files of that category in a
  singular subdirectory: `handler/`, `service/`, `model/`, or `client/`.
  Count files, not functions; exclude `_test.go` files from this threshold.
* If adding a fourth handler file, reorganize the existing handlers into
  `handler/` in the same change. Apply the same rule to services, models, and
  clients. Once a category has a subdirectory, add new files there.
* Subdirectories are also appropriate below the threshold when needed to keep
  dependencies acyclic or isolate transport from reusable business logic.
  Do not add empty directories or mandatory layers to every small module.
* Keep related operations together and use descriptive filenames. Split large
  files by responsibility (for example, record queries, transitions, and undo),
  rather than creating one file per function.
* Every Go directory is a package. Dependencies should flow from handlers to
  services to models; models must not import services or handlers. Cross-module
  model references are allowed, but avoid circular imports and keep ownership
  explicit. Use small consumer-owned interfaces when behavior needs decoupling.
* Outbound HTTP integrations belong in their module's `client/` package when
  separated; they are not incoming request handlers.
* `internal/app` owns application wiring, routes, configuration, and lifecycle.
  Keep the executable entry point small. Shared database, calendar, and HTTP
  helpers belong in focused packages (`database`, `calendar`, `httpx`), only
  when multiple modules actually need them. Do not add generic `utils` packages.
* Put tests beside the package they exercise. Use external `_test` packages for
  public behavior and the same package for private helpers. Reserve top-level
  `tests/` for scenarios intentionally spanning multiple modules.
* Keep embedded assets with their owning package. When moving files, update
  imports, tests, embed paths, build commands, and documentation together.
* Organization changes must preserve HTTP routes, JSON formats, database table
  and column names, and behavior unless a behavior change is explicitly requested.

## Go Style

* Run `gofmt` on all changed Go files.
* Use idiomatic Go names:

    * package names: short, lowercase, no underscores
    * interfaces: small and behavior-based
    * exported names: documented when they are part of public API
* Prefer returning `(value, error)` over special sentinel values.
* Wrap errors with context using `fmt.Errorf("...: %w", err)`.
* Keep interfaces close to the consumer, not the implementation.
* Prefer table-driven tests for multiple input/output cases.
* Prefer composition over inheritance-like patterns.
* Avoid clever code. Readability is more important than being compact.

## Backend/API Style

* HTTP handlers should be thin:

    * parse request
    * validate input
    * call service/business logic
    * write response
* Business logic should not depend directly on HTTP types unless necessary.
* Database code should be isolated from handlers.
* Use transactions when multiple database operations must succeed or fail together.
* Always check SQL errors.
* Do not build SQL queries by string concatenating user input.
* Return consistent JSON error responses.
* Validate request bodies before using them.

## Testing

Before finishing a task, run the relevant tests when possible:

```bash
go test ./...
```

For larger backend or concurrency changes, also consider:

```bash
go test -race ./...
go vet ./...
```

If the project has a configured linter, run it:

```bash
golangci-lint run
```

Do not claim tests passed unless they were actually run.

## Dependencies

* Prefer the standard library.
* Before adding a dependency, check whether the project already has a similar package.
* If adding a dependency is necessary, explain why.
* Do not change `go.mod` or `go.sum` unnecessarily.

## Code Generation Behavior

When modifying code:

1. Read nearby code first.
2. Match existing patterns.
3. Make the smallest correct change.
4. Add or update tests if behavior changes.
5. Run formatting and tests.
6. Summarize what changed and what was tested.

## Things to Avoid

* Do not create unnecessary `utils` packages.
* Do not overuse interfaces.
* Do not add logging everywhere without purpose.
* Do not swallow errors with `_`.
* Do not create background goroutines without a clear shutdown path.
* Do not mix HTTP, business logic, and database logic in one large function.
* Do not rewrite working code just to make it look different.
