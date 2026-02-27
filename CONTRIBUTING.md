# Contributing to Tenazas

## Quick Start

```bash
# Build
go build ./cmd/tenazas/

# Run tests
go test ./internal/...

# Run a specific package's tests
go test ./internal/engine/

# Build + test everything
make
```

## Project Layout

Tenazas follows standard Go project layout with `internal/` packages:

```
cmd/tenazas/main.go     ← Entrypoint (wires packages, parses flags)
internal/
  config/               ← Configuration loading
  events/               ← Event bus, audit types, task status
  models/               ← Shared domain types (Session, SkillGraph, etc.)
  storage/              ← Atomic JSON I/O, path utilities
  session/              ← Session lifecycle (CRUD, audit, listing)
  registry/             ← Instance-to-session mapping (flock-based)
  client/               ← Agent CLI backends (Gemini, Claude Code, etc.)
  onboard/              ← Interactive setup wizard, client detection
  engine/               ← Skill execution loop, thought parser
  skill/                ← Skill file loading and listing
  task/                 ← Task model, work subcommand
  heartbeat/            ← Background task runner
  telegram/             ← Telegram Bot API (zero-SDK)
  cli/                  ← Terminal REPL, raw mode, drawer
  formatter/            ← ANSI and HTML output formatters
```

## Package Dependency Rules

Packages are organized in layers. **A package may only import from the same layer or below.**

| Layer | Packages | May Import |
|-------|----------|------------|
| 0 | `events`, `models`, `storage`, `client`, `config` | stdlib only |
| 1 | `formatter`, `registry`, `skill`, `task`, `onboard` | Layer 0 |
| 2 | `session` | Layers 0–1 |
| 3 | `engine` | Layers 0–2 |
| 4 | `heartbeat`, `telegram`, `cli` | Layers 0–3 |
| 5 | `cmd/tenazas` | All layers |

**Never import a higher layer from a lower one.** If you need cross-layer communication, use:
- The `events.GlobalBus` for decoupled messaging
- Interfaces defined in `models` (e.g., `EngineInterface`)
- The `heartbeat.Notifier` interface for telegram decoupling

## Adding a New Client

To add support for a new coding-agent CLI:

1. **Create `internal/client/<name>.go`** implementing the `client.Client` interface:
   ```go
   type Client interface {
       Run(ctx context.Context, cwd string, sessionID string, prompt string, skilledPrompt string, onChunk func(Chunk)) error
   }
   ```
   - `ctx`: Cancellation context for the subprocess.
   - `cwd`: Working directory to set as `cmd.Dir`.
   - `sessionID`: Native session ID for `--resume` (empty on first run).
   - `prompt`: The raw user prompt.
   - `skilledPrompt`: The prompt enriched with skill context by the engine.
   - `onChunk`: Callback invoked for each parsed output chunk; normalize agent output into `Chunk{Type, SessionID, Content, Delta}`.

2. **Add an `init()` function** that registers the client with the global registry:
   ```go
   func init() {
       Register("<name>", func() Client { return &MyClient{} })
   }
   ```

3. **Add the binary mapping in `internal/onboard/onboard.go`** so the setup wizard can detect the new CLI:
   ```go
   // Known client → binary name
   {"<name>", "<binary-name>"}
   ```

4. **Test** with:
   ```bash
   go test ./internal/client/
   ```

## Adding a New Feature

### New command in CLI
1. Add the handler in `internal/cli/cli.go` → `handleCommand()` switch
2. Add tab-completion in `getCompletions()`
3. Write tests in `internal/cli/`

### New Telegram callback
1. Add the handler function in `internal/telegram/telegram.go`
2. Register it in the `handlers` map inside `HandleCallback()`
3. Write tests in `internal/telegram/`

### New audit event type
1. Add the constant in `internal/events/events.go`
2. Update formatters in `internal/formatter/formatter.go` (both Ansi and Html)
3. Update display logic in `telegram.shouldDisplay()` if needed

### New skill capability
1. Update `internal/skill/skill.go` for loading
2. Update `internal/engine/engine.go` for execution
3. Update `internal/models/models.go` if new fields are needed in `SkillGraph`

## Writing Tests

- Tests live **next to their code** (e.g., `internal/engine/engine_test.go`)
- Use `package <name>` (not `package <name>_test`) to access unexported fields
- Each package should be testable in isolation
- For Telegram tests, use `mockTgServer` from `internal/telegram/test_utils_test.go`
- For heartbeat tests, use a `mockNotifier` implementing `heartbeat.Notifier`

```bash
# Run all tests
go test ./internal/...

# Run with verbose output
go test -v ./internal/engine/

# Run a specific test
go test -run TestEngineSkillLoop ./internal/engine/
```

## Code Style

- **No external dependencies** except `google/uuid`
- **Export sparingly** — only what other packages need
- **Interfaces at boundaries** — define consumer interfaces in the consuming package or `models`
- **Atomic writes** — always use `storage.WriteJSON()` for JSON persistence
- **Flock before writes** — use `syscall.Flock` for any shared file mutation
- Use `filepath.Join` for all path construction

## Building

```bash
# Development build
go build -o bin/tenazas ./cmd/tenazas/

# Production build
make build

# Cross-compile
GOOS=linux GOARCH=amd64 go build -o bin/tenazas-linux ./cmd/tenazas/
```
