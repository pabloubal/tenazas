# Tenazas Skill Playbook 🦀

This guide explains how to design, build, and deploy autonomous **Skills** for Tenazas. A Skill is a state machine that orchestrates Gemini through a series of iterative tasks (Action Loops) and automated checks.

---

## 1. Skill Anatomy

Skills are **isolated**. Each skill lives in its own subdirectory under `skills/`.

```text
skills/
└── my_feature_dev/
    ├── skill.json          # The state machine definition
    ├── prompt_plan.md      # Long instructions for the Planner
    ├── prompt_coder.md     # Long instructions for the Coder
    └── scripts/
        └── verify.sh       # Custom verification logic
```

### The `skill.json` Structure
The core of every skill is a JSON graph:

```json
{
  "skill_name": "my_skill",
  "initial_state": "start_node",
  "max_loops": 10,
  "states": {
    "start_node": {
      "type": "action_loop",
      "session_role": "architect",
      "instruction": "@prompt_plan.md",
      "verify_cmd": "test -f plan.md",
      "next": "implementation_node"
    },
    "implementation_node": { ... }
  }
}
```

---

## 2. State Types

### `action_loop` (Iterative Reasoning)
The most common state. It follows a **Prompt -> Act -> Verify** cycle.
1.  **Prompt**: Gemini receives the `instruction`.
2.  **Act**: Gemini performs the task (writing code, creating files).
3.  **Verify**: Tenazas runs the `verify_cmd`.
    -   **Success (Exit 0)**: Transitions to the `next` state. The output of the command is passed as context to the next state.
    -   **Failure (Non-zero)**: Transitions to the `on_fail_route` (or retries). The error log is captured and sent back to Gemini as feedback.

### `tool` (Automated Execution)
A "headless" state that runs a shell command without LLM intervention. Useful for cleanup, git commits, or notification pings.
-   Example: `"command": "git add . && git commit -m 'chore: automated checkpoint'"`

---

## 3. High-Fidelity Feedback

Tenazas provides Gemini with deep technical context during failures.

### Placeholders
In your `on_fail_prompt`, you can use these placeholders to inject context:
-   `{{stderr}}`: Full error output (or `{{stdout}}` / `{{output}}`).
-   `{{exit_code}}`: The numerical exit status of the failed command.

### Smart Truncation
Tenazas captures up to **32KB** of output. If a test suite generates a massive log, the engine automatically preserves:
1.  The **beginning** (usually compilation/syntax errors).
2.  The **end** (usually the specific assertion that failed).

---

## 4. Asset Resolution (`@` Prefix)

To keep `skill.json` clean, use the `@` prefix to reference external files within the skill's directory:

-   **Instructions**: `"instruction": "@prompts/coder.md"`
-   **Scripts**: `"verify_cmd": "@scripts/test.sh"`

Tenazas resolves these paths relative to the skill's base directory and automatically reads the content (for instructions) or provides the full absolute path (for scripts).

---

## 5. Design Patterns for Skills

### The "Loopback" Pattern
To fix bugs iteratively, point the `on_fail_route` back to the current state.
```json
"step_coder": {
  "on_fail_route": "step_coder",
  "on_fail_prompt": "Tests failed. Fix the code:

```
{{stderr}}
```"
}
```

### The "Reviewer" Pattern
Add a final verification state with a different `session_role` to act as a second pair of eyes.
```json
"review": {
  "session_role": "senior_reviewer",
  "instruction": "@reviewer_checklist.md",
  "verify_cmd": "grep -q 'STATUS: APPROVED' .review"
}
```

### Path Anchoring
Remember that all `verify_cmd` and `tool` commands run inside the session's **CWD** (the project folder), NOT the skill folder. Always use `@` to reference skill-local scripts.

---

## 6. Deployment & CLI Commands

1.  Create your skill folder in `~/.tenazas/skills/` (or the local project `skills/`).
2.  Run `tenazas cli` to start the interface.
3.  Use the following commands to manage and run skills:

- `/skills`: List all available skills and their status.
- `/skills toggle <name>`: Enable or disable a specific skill.
- `/run <skill_name>`: Start a skill execution in the current session.
- `/intervene <retry|proceed_to_fail|abort>`: Manually resolve a state that requires human intervention.
- `/help`: Show a list of all available commands.

Alternatively, run a skill directly from the command line without entering the REPL:

```bash
tenazas run <skill_name>
```

This runs the skill non-interactively in YOLO mode, streams output to stdout, and exits with code 0 on success or 1 on failure. Useful for CI pipelines and scripting.
