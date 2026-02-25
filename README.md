# Tenazas 🦀

**Tenazas** (Spanish for "Pincers") is a high-performance, zero-dependency Go gateway for the `gemini` CLI. It bridges your local terminal with Telegram, providing a stateful, directory-aware reasoning environment that follows you from your desk to your phone.

## Features

- **Autonomous Skill System**: Multi-state action loops that allow Gemini to perform complex, iterative tasks like TDD, refactoring, and code review.
- **TDD Feature Development**: A specialized skill (`tdd_feature_dev`) that enforces Red-Green-Refactor cycles with automated test verification.
- **High-Fidelity Log Capturing**: Captures up to 32KB of verification output (preserving the beginning for compilation errors and the end for assertion failures), ensuring Gemini has the full context to fix bugs.
- **Isolated Skill Architecture**: Skills are self-contained directories with their own instructions, scripts, and assets, making them easy to share and version.
- **Decoupled Architecture**: Run the Telegram server and the CLI REPL independently.
- **Image Support**: Send photos from Telegram for full multimodal analysis (via `gemini`'s `read_file` tool).
- **Session-Local Storage**: Each session creates a `.tenazas` directory in its local workspace (`CWD`) for images and temporary data.
- **Seamless Handoff**: Start a task on your laptop, continue on Telegram while AFK.
- **Spatial Awareness**: Sessions are "anchored" to your local project directories. Gemini sees your files, even when you're prompting from your phone.
- **Zero-SDK Telegram**: Built with raw Go `net/http` for maximum speed and minimal footprint.
- **JSONL Streaming**: Native integration with the `gemini` CLI's stateful JSONL protocol.

## Installation

### Prerequisites
1.  **Go 1.21+** installed.
2.  **Gemini CLI** installed and available in your `PATH`.
3.  A **Telegram Bot Token** (from [@BotFather](https://t.me/botfather)).

### Build
```bash
git clone https://github.com/youruser/tenazas
cd tenazas
go build -o tenazas .
sudo mv tenazas /usr/local/bin/
```

## Configuration

Create `~/.tenazas/config.json`:

```json
{
  "storage_dir": "/Users/youruser/.tenazas",
  "telegram_token": "123456789:ABCDEF...",
  "allowed_user_ids": [12345678],
  "update_interval": 500,
  "gemini_bin_path": "gemini"
}
```

*You can also use environment variables: `TENAZAS_TG_TOKEN` and `TENAZAS_ALLOWED_IDS` (comma-separated).*

## Usage

Tenazas now uses a subcommand structure to separate the local interface from the gateway server.

### CLI (Local Interface)
- **Start New Session**: `./tenazas cli` (or just `./tenazas`). This anchors the session to your current directory and creates a `.tenazas` folder for local data.
- **Resume Session**: `./tenazas cli --resume` (presents a paginated list of sessions).

### Server (Telegram Gateway)
- **Start Server**: `./tenazas server`
- This runs the Telegram polling loop as a standalone process. It requires a valid `telegram_token` in your configuration.

### Telegram Interaction
- **Chatting**: Send a message to your bot. It will automatically attach to your **most recently active** session.
- **Multimodal**: Send an image with an optional caption. The image is saved to the session's local `.tenazas` directory and analyzed by Gemini.
- **Switch/Resume**: Send `/resume` to see a list of sessions and pick one.
- **YOLO Mode**: Send `/yolo` to toggle auto-approve mode for the current session.
- **Status**: The bot streams responses in real-time, buffering updates to respect Telegram's rate limits.

## Skill System

Tenazas includes an autonomous engine that can execute complex "Skills" defined as state graphs.

### CLI Commands
- `/skills`: List all available skills and their status.
- `/skills toggle <name>`: Enable or disable a specific skill.
- `/run <skill>`: Start a skill execution in the current session.
- `/intervene <retry|proceed_to_fail|abort>`: Manually resolve a state that requires human intervention.

### Autonomous TDD Workflow
The `tdd_feature_dev` skill follows a strict engineering lifecycle:
1.  **Plan**: Gemini reads the issue and writes a technical plan to `plan.md`.
2.  **Red Phase**: Gemini writes unit tests. Verification fails if the tests *pass* or fail to compile.
3.  **Green Phase**: Gemini writes the minimal implementation to make tests pass.
4.  **Refactor**: Gemini cleans up the code without breaking the tests.
5.  **Review**: A separate reviewer role inspects the code and provides feedback.

Tenazas ensures continuity by passing the full output (logs) of each phase to the next role, allowing the "Coder" to see exactly why the "Tester's" tests failed.

## How it Works

Tenazas acts as a stateful proxy. It maintains a registry of "Instances" (PIDs and ChatIDs). When a prompt or image arrives, Tenazas:
1.  Identifies the target session and its workspace (`CWD`).
2.  Ensures a local `.tenazas` directory exists for the session.
3.  Downloads any incoming images to that local directory.
4.  Sets the working directory (`cmd.Dir`) to the session's anchor path.
5.  Spawns `gemini --resume <SID> --output-format json-stream` with the provided prompt and image references.
6.  Parses the JSONL stream and forwards the content chunks to the active interface.

## License
MIT
