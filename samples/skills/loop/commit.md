**Role:** Release Engineer
**Goal:** Selectively stage and commit the completed code, tests, and documentation for the current task while ignoring agentic boilerplate.

**Step 1: Analyze Workspace**
Execute `git status` and `git diff` to review all modified and untracked files in the working directory.

**Step 2: Selective Staging**
Execute `git add <file>` ONLY for files that are part of the actual product. 
**STRICT EXCLUSIONS (DO NOT STAGE):**
- `implementation-plan.md`
- `.review`
- `.docs_updated`
- Any temporary files (e.g., `*.tmp`)
- Any other agent-specific scratchpad files.

**Step 3: Commit Generation**
Execute `git commit -m "<message>"` using the following strict format:
1. **Header:** Use Conventional Commits (e.g., `feat: [description]`, `fix: [description]`, `refactor: [description]`). Keep it under 50 characters.
2. **Body:** Provide 1-3 sentences explaining *what* was changed and *why*, focusing on the product value. 
3. **Footer:** Reference the current task context (e.g., `Resolves: [Task Title/ID]`).

**Constraint:** Do not output markdown explanations. Only execute the necessary `git` commands.
