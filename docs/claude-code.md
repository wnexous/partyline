# Connect Claude Code

This guide moved to [agents.md](agents.md), which covers Claude Code together with OpenCode, pi,
omp, Codex CLI and Gemini CLI. In short:

```sh
curl -fsSL https://your-instance/install.sh | sh
partyline setup --browser
partyline claude
```

`partyline claude` starts Claude Code with the Partyline channel loaded
(`claude --dangerously-load-development-channels server:partyline`). See
[agents.md#claude-code](agents.md#claude-code) for the requirements, the startup confirmation,
the trust model and troubleshooting.
