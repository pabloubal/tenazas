# Tenazas 🦀

<p align="center">
    <a href="https://www.youtube.com/watch?v=eL2DgQt7ifw">
      <picture>
          <source media="(prefers-color-scheme: light)" srcset="./assets/tenazas.png">
          <img src="./assets/tenazas.png" alt="Your clawdy friend" width="500">
      </picture>
    </a>
</p>

**Tenazas** (Spanish for "Pincers" or "Claws") is a high-performance, zero-dependency Go gateway for coding-agent CLIs. It supports multiple backends (Gemini, Claude Code, and more) via a pluggable client architecture, and bridges your local terminal with Telegram — providing a stateful, directory-aware reasoning environment that follows you from your desk to your phone.

## Features

- **Multi-Client Support**: Pluggable backends — Gemini, Claude Code, and extensible to more. Each session tracks which client it uses.
- **Model Tiers**: Generic `high` / `medium` / `low` tiers that map to each client's actual models. Configurable per-session and per-skill-state.
- **Cost Control**: Set budget caps at session level via `/budget` or at skill level in YAML. Claude enforces natively via `--max-budget-usd`; Gemini silently skips.
- **Permission Modes**: Unified `PLAN` / `AUTO_EDIT` / `YOLO` modes, mapped to each client's native flags.
- **Autonomous Skill System**: Multi-state action loops that allow agents to perform complex, iterative tasks like TDD, refactoring, and code review.
- **TDD Feature Development**: A specialized skill (`tdd_feature_dev`) that enforces Red-Green-Refactor cycles with automated test verification.
- **High-Fidelity Log Capturing**: Captures up to 32KB of verification output (preserving the beginning for compilation errors and the end for assertion failures), ensuring the agent has the full context to fix bugs.
- **Isolated Skill Architecture**: Skills are self-contained directories with their own instructions, scripts, and assets, making them easy to share and version.
- **Decoupled Architecture**: Run the Telegram server and the CLI REPL independently.
- **Image Support**: Send photos from Telegram for full multimodal analysis.
- **Session-Local Storage**: Each session creates a `.tenazas` directory in its local workspace (`CWD`) for images and temporary data.
- **Seamless Handoff**: Start a task on your laptop, continue on Telegram while AFK. Sessions remember which client they use.
- **Spatial Awareness**: Sessions are "anchored" to your local project directories. Your agent sees your files, even when you're prompting from your phone.
- **Zero-SDK Telegram**: Built with raw Go `net/http` for maximum speed and minimal footprint.
- **JSONL Streaming**: Native integration with each agent CLI's streaming JSON protocol.

## Installation

### Prerequisites

1.  **Go 1.21+** installed.
2.  At least one supported coding-agent CLI installed:
    - **Gemini CLI** (`gemini`) — [installation](https://github.com/google-gemini/gemini-cli)
    - **Claude Code** (`claude`) — [installation](https://docs.anthropic.com/en/docs/claude-code)
3.  (Optional) A **Telegram Bot Token** (from [@BotFather](https://t.me/botfather)) for remote access.

### Build

```bash
git clone https://github.com/youruser/tenazas
cd tenazas
go build -o tenazas ./cmd/tenazas/
sudo mv tenazas /usr/local/bin/
```

## Configuration

Run `tenazas onboard` for an interactive setup wizard that detects installed agent CLIs and walks you through the initial configuration.

Or create `~/.tenazas/config.json` manually:

```json
{
  "storage_dir": "/Users/youruser/.tenazas",
  "default_client": "gemini",
  "default_model_tier": "high",
  "clients": {
    "gemini": {
      "bin_path": "gemini",
      "models": { "high": "gemini-2.5-pro", "medium": "gemini-2.5-flash", "low": "gemini-2.0-flash-lite" }
    },
    "claude-code": {
      "bin_path": "claude",
      "models": { "high": "opus", "medium": "sonnet", "low": "haiku" }
    }
  },
  "channel": {
    "type": "telegram",
    "token": "123456789:ABCDEF...",
    "allowed_user_ids": [12345678],
    "update_interval": 500
  }
}
```

_You can also use environment variables: `TENAZAS_TG_TOKEN` and `TENAZAS_ALLOWED_IDS` (comma-separated)._

### Key Config Fields

| Field                      | Description                                                      |
| -------------------------- | ---------------------------------------------------------------- |
| `default_client`           | Agent backend for new sessions (`"gemini"`, `"claude-code"`)     |
| `default_model_tier`       | Default model tier for new sessions (`"high"`, `"medium"`, `"low"`) |
| `clients.<name>.bin_path`  | Path to the agent CLI binary                                     |
| `clients.<name>.models`    | Model tier mapping: `high`, `medium`, `low` → actual model names |
| `channel.type`             | Channel type: `"telegram"` or `"disabled"`                       |
| `channel.token`            | Telegram bot token                                               |
| `channel.allowed_user_ids` | Whitelisted Telegram user IDs                                    |
| `max_loops`                | Safety limit on autonomous skill iterations (default: 5)         |

## Usage

### CLI (Local Interface)

- **Start New Session**: `tenazas` — anchors the session to your current directory.
- **Resume Session**: `tenazas --resume` — presents a paginated list of sessions to pick from.

### Daemon (Telegram Gateway + Background Tasks)

- **Start Daemon**: `tenazas --daemon`
- This starts the Telegram polling loop and the heartbeat runner. Requires a valid `token` in your channel configuration.

### Telegram Interaction

- **Chatting**: Send a message to your bot. It will automatically attach to your **most recently active** session.
- **Multimodal**: Send an image with an optional caption. The image is saved to the session's local `.tenazas` directory and analyzed by the agent.
- **Sessions**: Send `/sessions` to browse and switch between sessions.
- **YOLO Mode**: Send `/yolo` to toggle auto-approve mode for the current session.
- **Run Skills**: Send `/run <skill>` to start a skill execution.
- **Audit Log**: Send `/last [n]` to view recent audit entries.
- **Verbosity**: Send `/verbosity` to toggle verbose output.
- **Help**: Send `/help` to see all available commands.

## Skill System

Tenazas includes an autonomous engine that can execute complex "Skills" defined as state graphs.

### CLI Commands

- `/run <skill>`: Start a skill execution in the current session.
- `/skills`: List all available skills and their status.
- `/skills toggle <name>`: Enable or disable a specific skill.
- `/mode <plan|auto_edit|yolo>`: Set the approval mode for the current session.
- `/budget [amount]`: Show or set the session budget cap (e.g. `/budget 5.00`, `/budget 0` for unlimited).
- `/intervene <retry|proceed_to_fail|abort>`: Manually resolve a state that requires human intervention.
- `/last [n]`: View recent audit log entries.
- `/help`: Show a list of all available commands.

### Autonomous TDD Workflow

The `tdd_feature_dev` skill follows a strict engineering lifecycle:

1.  **Plan**: The agent reads the issue and writes a technical plan to `plan.md`.
2.  **Red Phase**: The agent writes unit tests. Verification fails if the tests _pass_ or fail to compile.
3.  **Green Phase**: The agent writes the minimal implementation to make tests pass.
4.  **Refactor**: The agent cleans up the code without breaking the tests.
5.  **Review**: A separate reviewer role inspects the code and provides feedback.

Tenazas ensures continuity by passing the full output (logs) of each phase to the next role, allowing the "Coder" to see exactly why the "Tester's" tests failed.

## Subcommands

| Command | Description |
|---------|-------------|
| `tenazas` | Start the interactive CLI REPL (default) |
| `tenazas --resume` | Resume a previous session |
| `tenazas --daemon` | Start Telegram bot + heartbeat runner |
| `tenazas onboard` | Interactive setup wizard |
| `tenazas work` | Task management subcommand |

## How it Works

Tenazas acts as a stateful proxy. It maintains a registry of "Instances" (PIDs and ChatIDs). When a prompt or image arrives, Tenazas:

1.  Identifies the target session, its workspace (`CWD`), and its associated client.
2.  Ensures a local `.tenazas` directory exists for the session.
3.  Downloads any incoming images to that local directory.
4.  Sets the working directory (`cmd.Dir`) to the session's anchor path.
5.  Spawns the session's agent CLI (e.g., `gemini --resume <SID>` or `claude --continue <SID>`) with the configured model tier, approval mode, and budget.
6.  Parses the JSON stream and forwards the content chunks to the active interface.

## License

MIT
