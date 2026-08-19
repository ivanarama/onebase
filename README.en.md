# OneBase

**An open-source business application platform in Go.** Metadata describes your
domain objects, a built-in DSL describes the logic, and a single binary runs the
result on SQLite or PostgreSQL.

> **Heads up: this project is Russian-first.** The DSL keywords, the admin UI and
> nearly all documentation are in Russian, on purpose. This page explains what
> OneBase is and whether it is for you. [Русский README →](README.md)

---

## What it is

You describe an accounting-style application declaratively — catalogs
(reference data), documents, accumulation registers, a chart of accounts,
reports, managed forms — as YAML files. Business logic goes into `.os` modules
written in a 1C-like DSL. One binary (`onebase`) loads that configuration and
serves the whole application: web UI, REST API, background jobs, printing, and
a desktop launcher for managing databases.

The model will look familiar if you have worked with 1C:Enterprise, SAP Business
One, MS Dynamics NAV or similar mid-market accounting platforms: **posting**
documents produces register movements, registers give you balances and turnovers,
and reports query them through virtual tables.

| Layer | In OneBase | Physically |
|---|---|---|
| Platform | `onebase` binary | Go: runtime, DSL, REST API, launcher |
| Configuration | folder of YAML + `.os` files, or rows in the database | your application |
| Information base | SQLite file or PostgreSQL schema | application and service tables |

**Notable capabilities:** double-entry accounting registers with analytical
dimensions, FIFO inventory, a visual form designer, a report composition builder,
row-level access control, personal-data masking, full-text search, 2FA and SSO,
data exchange between bases, an LLM assistant wired into the UI, and self-update
from GitHub releases.

## Why Russian?

Because the audience it was built for thinks in Russian accounting terms. The
DSL is a deliberate near-clone of the 1C language — that is the whole point:
the several hundred thousand developers who know that language can write
`Если … Тогда … КонецЕсли` on day one, with no translation layer in their heads.

Renaming keywords to English would not make the platform international; it would
just break the one group it is genuinely useful to today. So the DSL stays
Russian, and this page exists so that everyone else can at least tell what is
going on here.

What that means in practice:

| | Language |
|---|---|
| Source code, comments, commit messages | Russian |
| DSL keywords and built-in function names | Russian (most have English aliases: `СтрЗаменить` / `StrReplace`) |
| Admin/user interface | 16 languages, English included |
| Documentation | Russian, except this page |
| REST API | English field names |

## Screenshots

The managed-form designer:

<img alt="Form designer" src="https://github.com/user-attachments/assets/a462d454-5859-4ea1-bb73-267623d2c73e" width="820">

Report composition builder:

<img alt="Report composition" src="https://github.com/user-attachments/assets/a18faf82-867c-446b-b604-e48c60679f9d" width="820">

The built-in LLM assistant, working against live application data:

<img alt="AI assistant" src="https://github.com/user-attachments/assets/cbfba341-2222-48f8-b80b-4b3de64971e4" width="820">

## Try it in three commands

Download a build for your platform from
[Releases](https://github.com/ivanarama/onebase/releases/latest) — Windows,
Linux and macOS, on both x86-64 and ARM. No database server required: SQLite is
compiled in.

```bash
tar xzf onebase-linux-amd64.tar.gz
cd onebase-linux-amd64

# Run the flagship example: warehouse management with FIFO costing
./onebase dev --project ./examples/trade --sqlite trade.db --open
```

The browser opens at `http://localhost:8080/ui` once the server is ready. There
are nine example configurations in `examples/` — trade, accounting, CRM,
personal finance, task tracking, a call centre, document registration, a CMS
website and a minimal teaching template.

On Windows, run `onebase-gui.exe` instead for a native launcher window.

## Where to look next

| | |
|---|---|
| [`docs/dsl-reference.md`](docs/dsl-reference.md) | **the whole language on one page** — every built-in function, object method, language construct and query-language element, with signatures and examples. The most useful page here if you do not read Russian: the code samples speak for themselves |
| [`docs/rest-api-v2.md`](docs/rest-api-v2.md) | REST API — English field names |
| [`examples/`](examples/) | nine working configurations to read |
| [`QUICKSTART.md`](QUICKSTART.md) | getting started (Russian) |
| [`DEVELOPER.md`](DEVELOPER.md) | full object-format reference (Russian) |
| [`CHANGELOG.md`](CHANGELOG.md) | what changed between versions |

Issues and pull requests in English are welcome — the maintainer reads and
replies in English.

## Building from source

Go 1.26.6 (pinned in `.go-version`). No CGo needed for the standard binary:

```bash
go build -o onebase ./cmd/onebase
go test ./...
```

The Windows GUI build (native WebView2 window) additionally needs CGo:

```bash
go build -tags webview -ldflags="-H windowsgui" -o onebase-gui.exe ./cmd/onebase
```

## License

[MIT](LICENSE) © 2026 Ivan Titov.

1C and 1C:Enterprise are trademarks of 1C LLC. OneBase is developed
independently, is not affiliated with 1C LLC and is not their product. The
import/export converters work only with publicly documented text formats (XML
and BSL), using original mapping tables written from public documentation.
