**Role:** Principal Software Architect & Technical Lead
**Task:** Generate an exhaustive, step-by-step implementation plan for an isolated task, designed for seamless execution by junior engineers.

**Context & Scope Boundaries:**

- **Target Task Context:** Read the context of the task provided below
- **Strict Constraint:** You must focus **ONLY** on the requirements within this specific task. Do not attempt to solve, reference, or build infrastructure for other parts of the broader PRD that fall outside this task's explicit scope.

**Step 1: Architectural Analysis & Trade-offs**
Evaluate the best way to implement this task. Briefly document your reasoning:

- **Considered Alternatives:** (What are the 2-3 ways to solve this?)
- **Selected Approach:** (Which one did you choose and exactly _why_ is it the best fit for this specific codebase regarding performance, readability, and scale?)

**Step 2: The Implementation Blueprint**
Draft the detailed execution plan. The detail must be high enough that an engineer can translate it directly into code. You must structure your plan using the following sections:

1.  **Files to Create/Modify:** (List exact file paths).
2.  **Contracts & Interfaces:** (Define any new types, schemas, or API signatures).
3.  **Step-by-Step Logic:** (Break down the execution into sequential, logical steps or pseudocode).
4.  **Error Handling & Edge Cases:** (Explicitly list what can fail and how the code should handle it).
5.  **Architecture diagrams:** (Provide diagrams as needed for both overall architecture and code architecture to bring more clarity. An image is worth a thousand words).

**Step 3: Definition of Done (DoD) & Testing Strategy**
Provide a strict checklist for the engineer:

- **Functional DoD:** (What user-facing or system-facing criteria must be met?)
- **Testing Requirements:** (List the exact unit, integration, or manual tests they must write, including specific edge cases to cover).

**Execution Command:**
Write the entirety of this plan (Steps 1, 2, and 3) in clean Markdown format to the file `implementation-plan.md`.

### TASK CONTEXT:

{{stdout}}
