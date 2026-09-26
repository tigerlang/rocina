# Rocina

<p align="center">
  <img src="rocina.gif" alt="Rocina banner">
</p>

Rocina turns any tool-calling LLM into a team: one chief agent plans and delegates,
subagents execute with real tools. It ships with a Bubble Tea TUI and a headless mode.

## Features

- Chief/subagent orchestration over a message bus with `wait_for_chief` / `stop_wait` handshakes.
- Shared coordination board: public and private todos, task routing, shared memory, checkpoints.
  Every tool call bumps the store revision, so all agents read one live board.
- Agentic tools: `bash_tool`, `terminal_tool`, `web_tool`, `read_file`, `write_file`,
  `edit_file`, `list_dir`, `grep_tool`.
- Coordination tools: `wait_for_chief`, `send_to_chief`, `send_to_sub`, `stop_wait`,
  `public_todo`, `private_todo`, `update_public_todo`, `update_private_todo`,
  `spawn_subagent`, `list_agents`, `broadcast`, `shared_memory`, `checkpoint`.
- Providers: OpenAI-compatible, Anthropic/Claude, OpenRouter, and any custom
  OpenAI-compatible endpoint. Responses stream.
- Live chat per agent: white assistant text with markdown (headings, lists, inline code,
  and fenced code blocks as filled gray blocks), gray collapsible `thinking`, and every
  tool call in a filled gray block with the actual command and result. Command output
  streams into the block as it is produced, and a truncated result expands on click.
  Provider errors (rate limits, transport) appear inline in light red as a short message,
  not the raw provider body. A right sidebar keeps a minimised chat for every agent; a
  spawned subagent floats out of the chief card, and switching agents rolls the focus
  highlight to the new card.
- Multiple sessions (`ctrl+n`, `ctrl+l`). Sessions and the subagents they spawned persist
  across restarts (`<data_dir>/sessions.json`, `<data_dir>/subagents.json`) and are removed
  only with `d` in the session list.
- Settings overlay (`ctrl+o` or "settings" in the sidebar) with Security, a config editor
  that applies on save and refreshes the live model, and a Hotkeys tab.
- Per-session usage: context (the last request) and total billed tokens, requests per
  minute, and cost when the provider reports it.
- Model picker in the TUI (`ctrl+a`): lists the provider's models plus any `models` from
  the config. `tab` switches the target between `chief` and `subagents`; the choice is
  applied live and saved to the config.
- Messages go to the focused agent (`tab` to switch). The first message on the landing
  page starts a new session.
- Working directories: `you are in:` shows the launch directory, and `agent now in:` tracks
  the focused agent's live shell directory in real time, starting from the workspace.
- Stop agents: press `esc` twice to interrupt the chief, a subagent, all subagents or all
  agents from a small panel above the input.
- Wake agents: the chief sees every agent's state and can revive a stuck or failed one with
  `wake_up` / `wake_up_all`.
- Security: block user paths and system paths on Linux, Windows and macOS, and optionally
  require approval for every terminal/file command.

## Build

    go build ./...

## Run

    export ROCINA_API_KEY=...
    go run . -provider openai -model "<model>"
    go run . -provider claude -model "<model>"
    go run . -provider openrouter -model "<vendor>/<model>"
    go run . -provider other -base-url "<https://host/v1>" -model "<model>"

Headless:

    go run . -headless -goal "fix the failing tests"

## Configuration

Rocina reads `./rocina.json`, then `~/.config/rocina/config.json`. Flags and `ROCINA_*`
environment variables override the file. See [guide.md](guide.md) for providers, keys and
per-agent models. Keep `rocina.json` out of version control when it holds a key.

## TUI keys

    enter                send
    shift+enter/ctrl+j   newline
    tab / shift+tab      switch agent
    ctrl+n               new session
    ctrl+l               list, switch and delete sessions (d)
    ctrl+o               settings (security, config, hotkeys)
    ctrl+a               pick a model (tab switches chief / subagents)
    esc esc              stop agents
    wheel                scroll the focused chat
    click "thinking"     expand or collapse the reasoning
    click an agent card  focus that agent
    ctrl+c               quit

Sending the first message plays a smooth transition from the launcher into the session
layout: the input moves to the bottom, the chat and the agent sidebar fade in.

## Tests

    go test ./tests/...

## License

MIT — see [LICENSE](LICENSE).
