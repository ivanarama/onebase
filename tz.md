# 🚀 Technical Specification: Open-source Business Platform (1C-like DSL)

## 0. Goal

Build an MVP of a backend platform for business applications with:

- metadata-driven architecture (catalogs, documents)
- custom DSL (1C-like syntax)
- runtime engine
- PostgreSQL storage
- CLI-based development
- cross-platform support
- IDE-independent workflow

---

# 🧱 1. High-Level Architecture

## 1.1 Components

The system MUST include:

1. Core Runtime (Go)
2. Metadata Engine
3. DSL Engine (interpreter for MVP)
4. Storage Layer (PostgreSQL)
5. CLI Tool
6. Dev Mode (hot reload)
7. Runtime Mode (production)

---

## 1.2 Tech Stack

- Language: Go
- Database: PostgreSQL
- CLI: cobra (or similar)
- DSL parser: custom (PEG / handwritten)

---

# 🌍 2. Core Requirements

## 2.1 Cross-platform

The system MUST:

- run on Windows, Linux, macOS
- compile into a single binary
- have no OS-specific dependencies

---

## 2.2 IDE Independence

The system MUST:

- be fully CLI-driven
- store everything in plain text files
- NOT depend on any IDE
- work from terminal only

---

## 2.3 File-based Project

Project structure MUST be:


/project-root
/config
/catalogs
/documents
/registers
/src
main.go


---

# 🗂 3. Metadata System

## 3.1 Supported Types (MVP)

### Catalog

```yaml
name: Counterparty
fields:
  - name: Name
    type: string
  - name: INN
    type: string
Document
name: Invoice
fields:
  - name: Number
    type: string
  - name: Date
    type: date
  - name: Counterparty
    type: reference:Counterparty
3.2 Requirements
load metadata from YAML
validate schema
generate DB schema automatically
🗄 4. Storage Layer
4.1 Requirements
PostgreSQL only
auto schema generation
basic operations:
insert
update
select
4.2 Example
CREATE TABLE invoice (
  id UUID PRIMARY KEY,
  number TEXT,
  date TIMESTAMP,
  counterparty_id UUID
);
⚙️ 5. Runtime Engine
5.1 Features
create objects (Catalog, Document)
save objects
event system
5.2 Supported Events (MVP)
OnWrite
5.3 Example API
doc := runtime.NewDocument("Invoice")
doc.Set("Number", "123")
doc.Save()
🧠 6. DSL Engine
6.1 Syntax (1C-like)
Procedure OnWrite()

  If this.Number = "" Then
    Error("Number is required");
  EndIf;

EndProcedure
6.2 Features
Procedure
If / Else
Variables
Access to this
Built-in functions (Error)
6.3 Execution

MVP MUST use:

interpreter
AST-based execution
🔄 7. DSL ↔ Runtime Integration

DSL MUST support:

access to current object (this)
read/write fields
call runtime functions
🛠 8. CLI Tool
8.1 Commands
app init
app dev
app run
app build
app migrate
8.2 Behavior
app dev
run server
watch file changes
hot reload metadata + DSL
app migrate
apply DB schema updates
🔥 9. Dev Mode

Requirements:

file watching
hot reload
clear logs
error with file + line

Example:

invoice.os:12: Error: Number is required
🚀 10. Runtime Mode
optimized execution
no hot reload
compiled binary
🌐 11. API Layer

Minimal REST API:

POST /documents/invoice
GET /documents/invoice/{id}
🧪 12. Test Scenario

System MUST support:

Create Counterparty
Create Invoice
OnWrite executes
Data saved in DB
📦 13. MVP Scope (STRICT)

Implement ONLY:

Catalog
Document
Basic DSL
PostgreSQL
CLI
OnWrite event
❌ 14. Out of Scope

DO NOT implement:

UI
Reports
Complex registers
Distributed systems
💡 15. Developer Experience

System MUST:

be git-friendly
use plain text only
support hot reload
produce clear error messages
🔮 16. Future Compatibility

DSL MUST:

be compatible with future LSP implementation
allow future transpiler support
📢 17. Instructions for Implementation

You are a senior Go architect.

Build an MVP backend platform with:

metadata-driven design
custom DSL
runtime engine
PostgreSQL storage
CLI tool

Constraints:

keep it minimal
working end-to-end example REQUIRED
clean modular architecture
avoid overengineering

Priorities:

Working runtime
DSL execution
Metadata loading
Database integration

Deliverables:

full project structure
working CLI
example configs
example DSL
instructions to run