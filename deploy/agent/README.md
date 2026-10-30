# Partyline agent image

`ghcr.io/wnexous/partyline-agent` is the image a container AI runs on (feature
`container-agents`, CA-04/CA-05): Debian slim with `tini`, `git`, `curl`, util-linux `script`,
Node LTS, bun, the partyline binary and the four supported harnesses at pinned versions.

| Harness | Package / source | Version |
| --- | --- | --- |
| Claude Code | npm `@anthropic-ai/claude-code` | 2.1.304 |
| pi | npm `@earendil-works/pi-coding-agent` | 1.4.0 |
| OpenCode | npm `opencode-ai` | 1.19.3 |
| omp | GitHub release `can1357/oh-my-pi` (`omp-linux-x64`, checksum-verified) | 18.12.3 |

The versions are `ARG`s at the top of the runtime stage of the [Dockerfile](Dockerfile); the
entrypoint never updates a harness (the harnesses' self-updaters are off), so a container always
runs what the image pinned. It runs as the unprivileged user `agent` (uid/gid 1000, home
`/home/agent`), holds no Docker socket, no secret and no login: everything sensitive arrives in
the environment at start.

The server never starts this image directly. `partyline runner` (T4) creates the container with
the arguments below and a named volume for `/workspace`.

## What the entrypoint does

[entrypoint.sh](entrypoint.sh) (POSIX `sh`, `set -eu`, tracing off):

1. validates `PARTYLINE_URL`, `PARTYLINE_TOKEN` and `PARTYLINE_HARNESS`, and that the harness has
   a credential variable set (below);
2. creates the session's folder (`PARTYLINE_FOLDER`, else `$PARTYLINE_WORKSPACE/repo` with a
   repository and `$PARTYLINE_WORKSPACE` without one) and, when `PARTYLINE_REPO` is set and the
   folder is empty, runs `git clone --depth 50 --branch "$PARTYLINE_REPO_REF"` into exactly that
   folder;
3. `cd`s into that folder and exports it as `PARTYLINE_FOLDER`, so the AI the CLI reports is the AI
   the container was created for, whatever the container's own working directory is;
4. logs the partyline CLI in and registers the harness in one non-interactive step:
   `partyline setup --url "$PARTYLINE_URL" --token-stdin --harness "$HARNESS" --always --yes`
   (writes `/home/agent/.config/partyline/config.json`, mode 0600, and the harness's
   extension/plugin/MCP entry);
5. loops `script -qfec "partyline $HARNESS --principal" /dev/null` in that folder, restarting with
   an exponential backoff of 1s doubling to 60s, reset after a session that stayed up for 10
   minutes.

`--principal` makes the harness session the AI's principal session, so Partyline pins the
harness's session id and a message always reaches this session. `--always` is a `partyline setup`
flag (the launcher has none): it records in the harness config that the Partyline extension,
plugin and MCP server act in every session of the container, including the tools processes the
harness starts itself.

### Claude Code's first-run questions

Claude Code asks three questions in its TUI before it accepts a message: the first-run theme,
whether it trusts the files of the folder, and whether it may use the `ANTHROPIC_API_KEY` it
found in the environment. Nobody can press a key inside a container, so the entrypoint seeds
those three answers in `~/.claude.json` (mode 0600) for the folder the session runs in: the
theme, `hasCompletedOnboarding`, `projects[<folder>].hasTrustDialogAccepted`, and — only when
`ANTHROPIC_API_KEY` is set — the last 20 characters of the key in `customApiKeyResponses`, which
is exactly what answering "Yes" writes. It seeds nothing about permissions: a tool call still
asks for approval. `CLAUDE_CODE_OAUTH_TOKEN` (the token `claude setup-token` prints, the
recommended credential) needs none of this.

### Unattended Claude Code

Claude Code 2.1.304 asks once per launch to acknowledge `--dangerously-load-development-channels`
(the flag `partyline claude` uses to load the Partyline channel), offering "I am using this for
local development" or "Exit". The answer is not stored, so a container session would stop on it.

There is no supported non-interactive way to pre-acknowledge it, checked against Claude Code
2.1.304 and its documentation:

- `--channels` only accepts the allowed plugin entries (`plugin:<name>@<marketplace>`); a raw
  `server:` entry still needs `--dangerously-load-development-channels`, and Partyline is not a
  plugin on the approved allowlist.
- The development flag "prompts for confirmation, so it takes effect in interactive sessions";
  with `-p` (or the Agent SDK) Claude Code ignores the flag and the channel does not register.
- `channelsEnabled` and `allowedChannelPlugins` (managed settings) gate availability and the
  plugin allowlist but do not remove the development confirmation, and no settings key or
  environment variable records the acknowledgement.

The entrypoint therefore falls back to the interactive mechanism the flag documents: it exports
`PARTYLINE_UNATTENDED=1`, and for `claude` it runs the session under `script` with a FIFO as stdin
and sends a few Enters through the pty while the harness starts (the prompt's default choice is
"I am using this for local development"). It types nothing else and answers no other prompt. The
design records the decision (`design.md`, "Unattended Claude Code"); when Anthropic adds a
supported pre-acknowledgement this block goes away.

## Environment contract

Required:

| Variable | Meaning |
| --- | --- |
| `PARTYLINE_URL` | The instance base URL, `http(s)://…`. No quotes, backslashes or whitespace. |
| `PARTYLINE_TOKEN` | An agent token (`pl_agent_…`), created with the container AI. Scoped to that AI. |
| `PARTYLINE_HARNESS` | One of `claude`, `opencode`, `pi`, `omp`. |

Optional:

| Variable | Default | Meaning |
| --- | --- | --- |
| `PARTYLINE_MACHINE` | the container's hostname | The AI's machine, `container:<container-name>`. The CLI reports it instead of the container's hostname (which belongs to the container runtime and changes with every container), so the session joins the AI the container was created for. |
| `PARTYLINE_FOLDER` | `$PARTYLINE_WORKSPACE/repo` with a repository, else `$PARTYLINE_WORKSPACE` | The AI's folder. The entrypoint works in exactly this folder and the CLI reports it. |
| `PARTYLINE_REPO` | unset | Repository cloned into the session's folder when it is empty (`https://…`). |
| `PARTYLINE_REPO_REF` | `main` | Branch or tag cloned from `PARTYLINE_REPO`. |
| `PARTYLINE_WORKSPACE` | `/workspace` | Where the workspace lives when `PARTYLINE_FOLDER` is not set: the session's folder is `$PARTYLINE_WORKSPACE/repo` with a repository, else `$PARTYLINE_WORKSPACE`. |

`PARTYLINE_MACHINE` and `PARTYLINE_FOLDER` are set by Partyline (`internal/containeragents`) from the
AI's own identity; a container started by hand needs both to join the AI it was created for. A
mapping of the owner's secrets can never replace them.

Harness credentials: exactly one of the variables in a row is enough; they are provider keys the
harness reads from the environment itself, so the entrypoint writes no credential file.

| Harness | `PARTYLINE_HARNESS` | Credential variables (any one) | Where it comes from |
| --- | --- | --- | --- |
| Claude Code | `claude` | `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN` | `claude setup-token` (subscription), or an Anthropic API key |
| OpenCode | `opencode` | `OPENCODE_API_KEY`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, `OPENROUTER_API_KEY` | the OpenCode Go key, or any provider key OpenCode supports |
| pi | `pi` | `OPENCODE_API_KEY`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, `OPENROUTER_API_KEY` | the OpenCode Go key, or any provider key pi supports |
| omp | `omp` | `OPENCODE_API_KEY`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, `OPENROUTER_API_KEY` | the OpenCode Go key, or any provider key omp supports |

The owner stores each of these in Partyline as a named secret and maps it to the variable name
when creating the container AI (CA-03, CA-04); the runner passes them in an env file with mode
0600, never on a command line (CA-08). To use a different provider than the default model needs,
add its key to the container AI's mapping — the entrypoint only requires that one of the variables
of the selected harness is present, and it names the missing variables instead of starting a
session that could not authenticate.

## Volumes

| Path | What lives there |
| --- | --- |
| `/workspace` | The workspace: `repo/` (the cloned repository) and everything the AI writes. Give each container AI its own named volume so a restart or a server reboot keeps it (CA-05). The session folder is inside it (`/workspace/repo`, else `/workspace`), so a clone and the AI's own files share the volume. |

The volume is created from this image, so a fresh named volume inherits `agent`'s ownership of
`/workspace`. A pre-existing directory mounted there that another uid owns makes `git clone`
fail; run it with `--user 1000:1000` or fix the ownership of the volume.

`/home/agent` holds the partyline login (`.config/partyline/config.json`, mode 0600) and the
harness configs. It is not a volume: the entrypoint recreates all of it on start, so a new
container gets a clean harness home.

## Running it by hand

Build the image from the repository root (the build context is the whole repository, exactly as
the release workflow builds it):

```sh
docker build -f deploy/agent/Dockerfile -t partyline-agent:dev .
```

Start a container against an instance with an agent token from the web app (**API tokens**, kind
agent). Keep secrets out of the shell history and out of the process list: pass them with
`--env-file`, which takes `KEY=value` lines and is read by Docker, not by the container's argv.

```sh
cat > /tmp/agent.env <<'EOF'
PARTYLINE_URL=https://partyline.example.com
PARTYLINE_TOKEN=pl_agent_example
PARTYLINE_HARNESS=claude
CLAUDE_CODE_OAUTH_TOKEN=example
PARTYLINE_REPO=https://github.com/example/repo
PARTYLINE_REPO_REF=main
EOF
chmod 600 /tmp/agent.env

docker run -d --name agent-test \
  --restart unless-stopped \
  --env-file /tmp/agent.env \
  --volume agent-test-workspace:/workspace \
  --memory 2g --cpus 2 \
  partyline-agent:dev

docker logs -f agent-test     # registration, then the harness session
docker stop agent-test && docker rm agent-test
rm -f /tmp/agent.env
```

The AI appears in the web app as the AI of the folder `repo` (the base name of the clone
directory, `/workspace/repo`), with this session as its principal one. Without `--restart`, a
harness exit is retried by the entrypoint's own backoff until the container is stopped.

[smoke.sh](smoke.sh) is the local check: it builds the image, runs `partyline version` and every
harness's `--version`, and asserts the entrypoint refuses an unknown harness.

## Release

`.github/workflows/release.yml` builds and pushes this image on every `v*` tag, with the same
tags as the server image (`{{version}}`, `{{major}}.{{minor}}` and `latest` for a final release),
for `linux/amd64` and `linux/arm64`.
