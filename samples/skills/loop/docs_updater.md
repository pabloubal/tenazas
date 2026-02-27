**Role:** Principal Technical Writer & Systems Architect
**Task:** Conduct a post-session documentation sync. You must translate raw code changes into accurate, well-integrated documentation updates without destroying existing context.

**Step 1: Context Gathering (Read-Only)**

1. Execute `git diff HEAD` to analyze all code changes made during this session.
2. Read the current contents of the target files (`AGENTS.md`, `README.md`, `docs/prd.md`) to understand their existing structure, tone, and active diagrams.

**Step 2: Impact Analysis (Chain-of-Thought)**
Before making any edits, internally map the `git diff` to the documentation:

- **What changed?** (New features, architectural shifts, config updates, new skills).
- **Which files are impacted?**
- **What specific sections need updating?** (e.g., "The API route changed in code, so the 'Usage' section in README.md needs a new curl example").

**Step 3: Surgical Execution (Write)**
Apply updates to the necessary files following these strict guidelines:

- **Preservation:** Do NOT rewrite entire documents. Surgically insert, modify, or delete only the sections directly affected by the diff. Leave unrelated text exactly as is.
- **Tone & Style:** Match the existing technical voice. Keep descriptions concise but comprehensive.
- **Visuals:** If architectural dependencies changed, you must update the syntax of any affected Mermaid (````mermaid`) diagrams.
- **Omission:** If a file requires no changes based on the diff, do not touch it.

**Step 4: Verification & Closure**
Once all necessary files are saved and verified, create a file named `.docs_updated` in the root directory containing exactly the text: `DOCS: UPDATED`.
