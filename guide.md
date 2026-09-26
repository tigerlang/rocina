# Rocina guide

## Providers

| Kind | Flag | Base URL | Notes |
|---|---|---|---|
| `openai` | `-provider openai` | `https://api.openai.com/v1` or `-base-url` | OpenAI and compatible |
| `claude` | `-provider claude` | `https://api.anthropic.com` | Anthropic `/v1/messages` |
| `openrouter` | `-provider openrouter` | `https://openrouter.ai/api/v1` | built in, adds referer/title headers |
| `other` | `-provider other -base-url ...` | yours | any OpenAI-compatible endpoint |

An OpenAI-compatible gateway or a local server runs as `other`.

## Custom OpenAI-compatible endpoint

    export ROCINA_API_KEY="<key>"
    go run . -provider other \
      -base-url "<https://host/v1>" \
      -model "<model>" \
      -chief-model "<model>" \
      -sub-model "<model>"

An empty api key is valid for endpoints that do not require one.

## OpenRouter

    export ROCINA_API_KEY="<key>"
    go run . -provider openrouter -model "<vendor>/<model>"

Model slugs come from the OpenRouter model list.

## Local models

    go run . -provider other -base-url "http://localhost:<port>/v1" -model "<model>"

## Config file

Discovery order: `./rocina.json`, then `~/.config/rocina/config.json`. An explicit
`-config <path>` skips discovery and fails if the file is missing.

    {
      "provider": "other",
      "base_url": "<https://host/v1>",
      "api_key": "<key>",
      "model": "<model>",
      "chief_model": "<model>",
      "sub_model": "<model>",
      "workspace": ".",
      "data_dir": "/home/you/.rocina",
      "max_subagents": 4,
      "max_steps": 40,
      "models": ["model-a", "model-b"],
      "temperature": 0.2
    }

`provider`, `model` and (for `other`) `base_url` are required. `chief_model` and
`sub_model` fall back to `model`.

## API keys

Three ways, highest priority first:

1. `-api-key <key>` flag.
2. `ROCINA_API_KEY` environment variable.
3. `"api_key"` in the JSON config.

Set the key once per shell:

    export ROCINA_API_KEY="<key>"

or paste it into the config:

    "api_key": "sk-xxxxxxxx"

Keep the key out of git when the config lives in a repository.

## Environment variables

`ROCINA_PROVIDER`, `ROCINA_API_KEY`, `ROCINA_BASE_URL`, `ROCINA_MODEL`,
`ROCINA_CHIEF_MODEL`, `ROCINA_SUB_MODEL`, `ROCINA_WORKSPACE`, `ROCINA_DATA_DIR`,
`ROCINA_GOAL`, `ROCINA_HEADLESS=1`.

## Flags

    -config          path to a json config
    -provider        openai | claude | openrouter | other
    -api-key         api key
    -base-url        base url override
    -model           default model
    -chief-model     chief model
    -sub-model       subagent model
    -workspace       working directory for file and shell tools
    -data-dir        state directory (default ~/.rocina)
    -goal            initial goal for the chief
    -headless        run without the TUI
    -max-subagents   subagent limit
    -max-steps       tool steps per turn
    -install-fonts   install the recommended fonts and exit
    -version, -v     print version

## Fonts

Rocina recommends monospace fonts but a terminal app cannot switch the terminal's font
itself, so it installs them for the terminal to pick up. On start it best-effort registers
Monaspace Neon, then JetBrains Mono, and if neither can be installed it leaves the system
default. Run `rocina -install-fonts` to do it explicitly and see the result; already
installed fonts are detected and reused. After installing, select the font in your
terminal settings.

## Choosing models

- `-model` applies to everyone.
- `-chief-model` overrides the chief only; use a stronger model for planning.
- `-sub-model` overrides every subagent; use a cheaper model for execution.
- `spawn_subagent` accepts a `model` argument for one specific subagent.
- `ctrl+a` opens a picker with the provider's models (from its models endpoint) merged
  with the `models` list in the config. `tab` switches the target between `chief` and
  `subagents` (shown in the title); `enter` or click applies it live and writes the chosen
  model to the config file.
- The active provider and model are shown under the input, along with the working
  directories.

## Tools

The chief gets: `send_to_sub`, `stop_wait`, `spawn_subagent`, `broadcast`, `wake_up`, `wake_up_all`.
Subagents get: `wait_for_chief`, `send_to_chief`.
Both get: `public_todo`, `private_todo`, `update_public_todo`, `update_private_todo`,
`list_agents`, `shared_memory`, `checkpoint`, `think`, plus the base tools below.

| Tool | Purpose |
|---|---|
| `bash_tool` | run a shell command, returns output and exit status |
| `terminal_tool` | persistent shell; cwd and exports survive between calls |
| `web_tool` | HTTP request, returns status and body |
| `read_file` / `write_file` / `edit_file` | file access |
| `list_dir` / `grep_tool` | directory listing and regex search |
| `wait_for_chief` | subagent blocks until the chief sends data |
| `send_to_chief` | subagent reports a result or question |
| `send_to_sub` / `stop_wait` | chief delegates or releases a waiting subagent |
| `spawn_subagent` | chief creates a worker with a role and optional model |
| `public_todo` / `update_public_todo` | shared plan on every agent's board |
| `private_todo` / `update_private_todo` | per-agent scratch list |
| `shared_memory` | get/set/list/delete keys on the shared blackboard |
| `list_agents` | agent states and todo counts |
| `broadcast` | message every other agent |
| `wake_up` | chief revives one stuck or failed agent and restarts its loop |
| `wake_up_all` | chief revives every inactive or failed subagent |
| `checkpoint` | save/load/list an agent's conversation |
| `think` | short reasoning step with no side effects |

## TUI

The session view streams everything an agent does. Assistant text is white, `thinking`
is gray and expands on click, and each tool call shows its arguments (the executed
command) and its result. Command output appears live while `bash_tool` or `terminal_tool`
runs, and a result truncated with `… (N more lines)` expands on click. A provider error
is shown as one short message rather than the raw JSON the endpoint returned. The right sidebar keeps a minimised chat per agent;
`tab` switches the focused agent, and a spawned subagent floats out of the chief card
into the sidebar. Switching agents rolls the focus highlight from the old card to the
new one. Sending the first message plays a smooth transition from the launcher to the
session layout.

Input always goes to the focused agent: `tab` to `chief`, a subagent or any other agent
and press `enter`. On the landing page there is no focused agent yet, so the first message
starts a new session instead of continuing the last one.

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

## Sessions

`ctrl+n` opens a fresh session: a new chief, bus and store under
`<data_dir>/sessions/<id>`. Sessions persist across restarts: the list and the active one
are stored in `<data_dir>/sessions.json` and restored on start, and the subagents a
session spawned are saved in `<data_dir>/subagents.json` and rebuilt with their history.
`ctrl+l` lists every session; select with arrows and `enter`, or click a row. `d` (or
`delete`/`backspace`) removes the selected session, stops its agents and deletes its
directory. Each session keeps its own chats, agents and history.

## Working directories

Above the input, `you are in:` shows the directory Rocina was started from. In a session,
`agent now in:` shows the focused agent's live shell directory: the persistent shell
starts in the configured workspace and `terminal_tool` reads `$PWD` after every command,
so once the agent creates a folder and `cd`s into it the path updates in real time.
Before the shell is used it falls back to the absolute workspace.

## Usage

Under the input the session shows `ctx` (prompt tokens carried by one request), `total`
billed tokens, request count, requests per minute, and cost. Token counts come from the
usage each provider reports per request; streaming usage is merged so a provider that
repeats or splits its usage report is not double counted. Cost is shown only when the
provider reports it (for example OpenRouter), otherwise it reads `cost n/a`.

Long-running agents keep their prompt bounded: every tool result — including command
output from `bash_tool` and `terminal_tool` — is capped before it enters the
conversation, the conversation history sent to the model is trimmed to a character
budget, and the coordination board is a capped summary. Each of these is
resent on every step, so their caps decide the fixed per-request cost. The original
goal and the most recent messages are always preserved, and no tool result is ever left
without its call. This keeps one long run from billing millions of tokens for resending
an ever-growing log on every step.

## Stopping agents

Press `esc` twice while at least one agent is running or waiting to open a small panel
above the input. `tab` / arrows / mouse pick `chief`, `Subagent…`, `all subagents` or
`all agents`; `enter` stops, `esc` closes. `Subagent…` opens the list of running
subagents by name. Stopping cancels the agent's context and marks it stopped.

## Waking agents

A unique Rocina feature: the chief sees the state of every agent and can bring a broken
one back. When a subagent is in `error`, loops forever in thinking, or a terminal command
never returns, the chief calls `wake_up` with the agent's name (optionally with a fresh
instruction) or `wake_up_all` to revive every inactive or failed subagent at once. Waking
cancels any leftover run, clears the failure state and restarts the agent's loop, so a
stuck team recovers without a restart. Only the chief holds these tools; use `list_agents`
to see who needs waking.

## Settings

`ctrl+o` or the sidebar "settings" opens the settings overlay.

- **security** — three toggles, applied immediately and saved to
  `~/.config/rocina/config.json`:
  - `Block user paths` denies file access outside the workspace and into personal
    directories. On Linux and macOS: home and its Documents, Desktop, Downloads,
    Pictures, Music, Videos, Public, Templates, Library, plus private dirs such as `.ssh`,
    `.gnupg`, `.aws`, `.kube`, `.docker`, `.mozilla`, `.thunderbird` and history files;
    extra roots are read from `~/.config/user-dirs.dirs`, and `/home`, `/Users` are
    covered. On Windows: the user profile, `AppData`, `OneDrive` and `C:\Users`.
  - `Block system paths` denies OS-owned locations: `/etc`, `/bin`, `/sbin`, `/usr`,
    `/lib*`, `/boot`, `/dev`, `/proc`, `/sys`, `/root`, `/var`, `/opt`, `/srv`, `/run` on
    Linux; `/System`, `/Library`, `/usr`, `/bin`, `/sbin`, `/private` and more on macOS;
    `%SystemRoot%`, `%ProgramFiles%`, `%ProgramData%` and `C:\Windows` on Windows.
  - `Always ask before running a terminal/file command` shows a permission prompt for
    `bash_tool`, `terminal_tool`, `write_file` and `edit_file`. In headless mode with no
    approver such calls are denied.
- **tui config** — an embedded editor pre-filled with the full config. `ctrl+s` writes it
  to `~/.config/rocina/config.json` and applies it immediately: the provider is rebuilt
  and the model, workspace, limits and security are updated for every running session.
- **hotkeys** — a read-only list of every keybinding. Switching tabs slides an underline
  to the selected one, matching the agent sidebar.

## Security

The policy lives in the config:

    "security": {
      "block_user_paths": true,
      "block_system_paths": true,
      "ask_before_run": true,
      "allowed_roots": ["/srv/shared"]
    }

Paths inside `workspace` and `allowed_roots` stay reachable even when the path blocks are
on.

## Coordination flow

1. The chief plans and writes `public_todo` entries.
2. `spawn_subagent` creates workers; `send_to_sub` assigns tasks.
3. A subagent works, reports with `send_to_chief`, then calls `wait_for_chief` to block.
4. The chief reads the report, updates the board, and either assigns more work with
   `stop_wait` or finishes.

Each tool call is journaled in the store and bumps the revision, so every agent's next
LLM turn sees the current board.

The board only shows open todos (completed ones drop off) and is capped, so context does
not grow with the number of tasks. Routed messages (`send_to_sub`, `stop_wait`,
`send_to_chief`) travel only through the message bus and are not duplicated on the board,
so each payload is paid for once. While the chief waits on subagents it idles without
spending model requests; it wakes on the next report.

## Troubleshooting

- `shift+enter` does not insert a newline on most terminals: use `ctrl+j`. `shift+enter`
  works only on terminals with the Kitty keyboard protocol.
- `401` from the provider: the api key is missing or wrong for the selected base URL.
- `model is not configured`: pass `-model`, or set both `chief_model` and `sub_model`.
- `a base url is required for the other provider`: pass `-base-url` or set it in the config.
