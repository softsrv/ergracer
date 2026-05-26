# softsrv starter repo

A standard architecture template for LLM-assisted app development — designed to produce a consistent, production-ready Go web app on the first pass.

## Setup

- Copy `architecture.md` into your project, or place it somewhere your agent can access it.
- If your tool supports system prompts (Claude Projects, Cursor rules, `.cursorrules`, etc.), paste the full file there so it's in context for every request.

## Prompting Tips

- **Set the role:** Start with something like "You are a senior Go engineer."
- **Reference the file directly:** "Build the application described in `architecture.md`."
- **Give the full file — don't summarize it.** The spec is intentionally detailed; summarizing it loses constraints the LLM needs.
- **Ask for a full implementation in one request.** The spec is detailed enough that a single pass works well. Get the app running first, then add features incrementally.
- **Watch for stubs.** Long outputs can cause LLMs to leave `// TODO` placeholders in complex areas (background jobs, test boilerplate). Follow up on those specifically if needed.
- **Paste it at the start of each new session.** LLMs don't carry context between sessions — always re-inject the spec.
