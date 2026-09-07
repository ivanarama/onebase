<div align="center">

# OneBase

**An open-source business application platform in Go.**

Metadata describes your domain objects, a built-in DSL describes the logic,
and a single binary runs the result on SQLite or PostgreSQL.

[Русский](README.md) · **English**

[Project site](https://onebase.ivantitov.tech) · [Live demo](https://demo.ivantitov.tech) · [Telegram](https://t.me/IvanTitovTech) · [Docs](QUICKSTART.md)

[![Latest release](https://img.shields.io/github/v/release/ivanarama/onebase?label=release)](https://github.com/ivanarama/onebase/releases/latest)
[![License MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev/)

![Trade configuration dashboard: KPI tiles, charts, recent documents](docs/images/dashboard.png)

</div>

> **Heads up: this project is Russian-first.** The documentation, the examples
> and the runtime messages are in Russian, on purpose — though the DSL itself
> reads in either language, see [below](#is-the-language-really-russian). This
> page explains what OneBase is and whether it is for you.

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

Renaming those keywords outright would not make the platform international; it
would just break the one group it is genuinely useful to today. So the Russian
spelling stays the default — and an English spelling was added next to it rather
than instead of it.

### Is the language really Russian?

Only by default. Every keyword has an English synonym, and a module can be
written entirely in English:

```bsl
Var total; total = 0;
For each row in rows Do
    If row.Qty > 0 And Not row.Cancelled Then
        total = total + row.Qty;
    EndIf;
EndDo;
Return total;
```

The same holds for the access objects (`Documents`, `Catalogs`, `Query`) and the
built-in functions (`Message`, `Str`, `StrReplace`): 370 names of the language
carry an English synonym. Mixing both spellings in one file is allowed and case
does not matter, because these are synonyms in the lexer's keyword table, not a
separate "English mode".

What stays Russian is the surroundings, and that is the real barrier:

| | Language |
|---|---|
| DSL keywords, built-in functions, access objects | **Both** — `If`/`Если`, `StrReplace`/`СтрЗаменить`, `Documents`/`Документы` |
| Runtime and syntax error messages | Russian |
| Type names in output (`Неопределено`, `Массив`) | Russian |
| Documentation, example configurations | Russian, except this page |
| Source code, comments, commit messages | Russian |
| Admin/user interface | 16 languages, English included |
| REST API | English field names |

## Screenshots

The dashboard above is a configuration at work, in a native window: monthly KPI
tiles, charts, recent documents.

The launcher — the entry point: SQLite and PostgreSQL bases, start/stop, and the
configurator:

![Database launcher](docs/images/launcher.png)

A document form — tabs, a SlickGrid table part with item picking and
recalculation, posting buttons, register movements in the header, attachments:

![Sales document form with a table part](docs/images/document-form.png)

The built-in LLM assistant, answering against live application data — here it
reads stock levels and margins, ranks what to reorder, and offers to draft the
purchase order:

![LLM assistant inside the application](docs/images/ai-assistant.png)

The managed-form designer — a live canvas, element properties and event bindings
on the right, with the form's YAML and module a tab away:

![Managed-form designer](docs/images/form-designer.png)

A report with grouping and per-level totals:

![Report with grouping and totals](docs/images/report-result.png)

Reports are composed, not hard-coded: the end user ticks which fields group and
which are measures, adds filters, changes the styling and saves it as a personal
variant. The configuration is untouched — the settings live in the database, per
user:

![End-user report settings: groupings, measures, filters, saved variants](docs/images/report-settings.png)

## Try it without installing anything

**[demo.ivantitov.tech](https://demo.ivantitov.tech)** — a deployed trade
configuration with demo data. The login page offers a list of users, each with a
different role and a different set of sections; the password for all of them is
`12345`. Pick the first one (Демонов) if you have no reason to prefer another.
The base runs in demo mode and resets every night at 02:00, so feel free to
break things.

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

## Two ways to build a configuration

**As files under git.** The configuration is a folder of YAML and `.os` files
(`config_source: file`) — an ordinary repository with branches, diffs, review
and history. `onebase dev` serves it with hot reload: save a file and the open
page re-reads itself, no refresh.

**In the database, through the configurator.** The configuration lives in the
`_onebase_config` table of the base itself (`config_source: database`) and is
edited by a metadata tree and visual editors inside the launcher. The user never
needs to know where a folder is — the base is one self-contained SQLite file.

You can move between the two: the configurator's *Выгрузить* (export) writes the
configuration out to a working folder and *Загрузить* (import) puts the edits
back, so even a base that lives on a user's machine can be kept in git.

## Built to be driven by an AI agent

This is deliberate, not a marketing line:

- **`onebase init` writes an `AGENTS.md` into the project** — 990 lines
  generated from the platform itself: configuration layout, the working loop,
  every DSL built-in, the metadata schema, the security model. An agent reads
  the language instead of guessing it from examples. Refresh it later with
  `onebase ai-guide --output AGENTS.md`.
- **`onebase check` is real feedback.** It compiles *and executes* the modules'
  queries, so it catches `no such column` before the base is ever started — the
  agent gets a concrete error with a location, not a plausible-looking guess.
- **`onebase mcp` exposes the same commands over MCP:** `check`, `query`,
  `describe`, `config_diff`, `config_versions`, `fmt_check`, `procrun`.
  Read-only by default; mutating tools are enabled per tool
  (`--allow-fmt-write`, `--allow-refactor-write`, `--allow-config-rollback`,
  `--allow-procrun`) or all at once with `--allow-write`.
- **The in-app LLM assistant** answers against live data, honouring the current
  user's permissions.

## Where to look next

| | |
|---|---|
| [onebase.ivantitov.tech](https://onebase.ivantitov.tech/docs.html) | searchable catalogue of everything the platform does, with a "currently in testing" filter (Russian) |
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
