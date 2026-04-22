# onebase

MVP metadata-driven business platform with a 1C-like DSL, built in Go.

## Quick start

```bash
# 1. Install Go 1.22+, PostgreSQL

# 2. Get dependencies
go mod tidy

# 3. Build
go build -o onebase ./cmd/onebase

# 4. Run with the example project
export DATABASE_URL=postgres://localhost/onebase_dev

./onebase migrate --project ./examples/simple-erp
./onebase dev    --project ./examples/simple-erp
```

## Testing

```bash
# Unit tests (no DB required)
go test ./...

# Integration test (requires PostgreSQL)
export TEST_DATABASE_URL=postgres://localhost/onebase_test
go test -tags=integration ./...
```

## End-to-end example

```bash
# Create a counterparty
curl -X POST localhost:8080/catalogs/counterparty \
  -H 'Content-Type: application/json' \
  -d '{"Name":"Acme Corp","INN":"1234567890"}'
# → {"id":"<uuid>"}

# Create an invoice with empty Number → OnWrite returns 422
curl -X POST localhost:8080/documents/invoice \
  -H 'Content-Type: application/json' \
  -d '{"Number":"","Date":"2026-04-22T00:00:00Z"}'
# → {"error":"Number is required","file":"...invoice.os","line":3}

# Create a valid invoice
curl -X POST localhost:8080/documents/invoice \
  -H 'Content-Type: application/json' \
  -d '{"Number":"INV-001","Date":"2026-04-22T00:00:00Z"}'
# → {"id":"<uuid>"}

# Fetch it back
curl localhost:8080/documents/invoice/<id>
```

## Project structure (user app)

```
my-app/
├── catalogs/     # YAML entity definitions (Catalog kind)
├── documents/    # YAML entity definitions (Document kind)
├── src/          # DSL handlers (*.os files, named after entity)
└── config/       # app config
```

## DSL syntax

```
Procedure OnWrite()
  If this.Number = "" Then
    Error("Number is required");
  EndIf;
EndProcedure
```

## CLI commands

| Command | Description |
|---------|-------------|
| `onebase init [dir]` | Scaffold new project |
| `onebase dev --project <dir>` | Dev server with hot reload |
| `onebase run --project <dir>` | Production server |
| `onebase migrate --project <dir>` | Apply DB schema |
