#!/usr/bin/env python3
"""Interactive setup CLI for the CustomClaw platform.

Usage:
    uv run setup.py
    python setup.py
"""

import getpass
import secrets
import subprocess
import sys
from pathlib import Path

# ── ANSI color helpers ──────────────────────────────────────────────────────

BOLD = "\033[1m"
GREEN = "\033[32m"
YELLOW = "\033[33m"
CYAN = "\033[36m"
RESET = "\033[0m"


def bold(text: str) -> str:
    return f"{BOLD}{text}{RESET}"


def green(text: str) -> str:
    return f"{GREEN}{text}{RESET}"


def yellow(text: str) -> str:
    return f"{YELLOW}{text}{RESET}"


def cyan(text: str) -> str:
    return f"{CYAN}{text}{RESET}"


# ── Input helpers ───────────────────────────────────────────────────────────

def prompt(label: str, default: str | None = None, secret: bool = False) -> str:
    """Prompt user for input, optionally masking the value."""
    hint = f" [{default}]" if default is not None else ""
    display = f"  {label}{hint}: "
    if secret:
        value = getpass.getpass(display)
    else:
        value = input(display)
    return value.strip() if value.strip() else (default or "")


def prompt_yn(label: str, default: bool = True) -> bool:
    """Prompt user for a yes/no answer."""
    hint = "Y/n" if default else "y/N"
    value = input(f"  {label} ({hint}): ").strip().lower()
    if not value:
        return default
    return value in ("y", "yes")


# ── Section header ──────────────────────────────────────────────────────────

def print_header(title: str) -> None:
    print(f"\n{bold(cyan('══'))} {bold(title)} {bold(cyan('══'))}\n")


def print_step(n: int, title: str) -> None:
    print(f"\n{bold(green(f'Step {n}:'))} {bold(title)}")
    print("─" * 50)


# ── Env file helpers ────────────────────────────────────────────────────────

def read_existing_env(path: Path) -> dict[str, str]:
    """Parse an existing .env file into a dict (ignores comments/blanks)."""
    values: dict[str, str] = {}
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        if "=" in line:
            key, _, value = line.partition("=")
            values[key.strip()] = value.strip()
    return values


def write_env(path: Path, values: dict[str, str]) -> None:
    """Write .env with section comments matching .env.example layout."""
    home = str(Path.home())

    def expand(v: str) -> str:
        return v.replace(home, "~")

    sections = [
        ("Database", [
            ("DB_USER", values.get("DB_USER", "customclaw")),
            ("DB_PASSWORD", values.get("DB_PASSWORD", "changeme")),
        ]),
        ("OpenSearch", [
            ("OPENSEARCH_ADMIN_PASSWORD",
             values.get("OPENSEARCH_ADMIN_PASSWORD", "changeme")),
        ]),
        ("LLM Providers", [
            ("ANTHROPIC_API_KEY", values.get("ANTHROPIC_API_KEY", "")),
            ("VOYAGE_API_KEY", values.get("VOYAGE_API_KEY", "")),
        ]),
        (
            "Slack (first bot — additional bots configured via bots/*.yaml)",
            [
                ("CUSTOMCLAW_SLACK_APP_TOKEN",
                 values.get("CUSTOMCLAW_SLACK_APP_TOKEN", "xapp-...")),
                ("CUSTOMCLAW_SLACK_BOT_TOKEN",
                 values.get("CUSTOMCLAW_SLACK_BOT_TOKEN", "xoxb-...")),
            ],
        ),
        ("GitHub", [
            ("GITHUB_TOKEN", values.get("GITHUB_TOKEN", "")),
        ]),
        ("Airflow", [
            ("AIRFLOW_SECRET_KEY", values.get("AIRFLOW_SECRET_KEY", "")),
            ("AIRFLOW_API_USER", values.get("AIRFLOW_API_USER", "admin")),
            ("AIRFLOW_API_PASSWORD", values.get("AIRFLOW_API_PASSWORD", "")),
        ]),
        ("Web UI (GitHub OAuth)", [
            ("NEXTAUTH_URL", values.get("NEXTAUTH_URL", "http://localhost:3000")),
            ("NEXTAUTH_SECRET", values.get("NEXTAUTH_SECRET", "")),
            ("GITHUB_CLIENT_ID", values.get("GITHUB_CLIENT_ID", "")),
            ("GITHUB_CLIENT_SECRET", values.get("GITHUB_CLIENT_SECRET", "")),
        ]),
        ("Security", [
            ("ENCRYPTION_KEY", values.get("ENCRYPTION_KEY", "")),
        ]),
        ("Host Paths (adjust to your system)", [
            ("HOST_WORKSPACE_PATH",
             expand(values.get("HOST_WORKSPACE_PATH", "~/workspace"))),
            ("CLAUDE_CLI_BINARY",
             expand(values.get("CLAUDE_CLI_BINARY", "~/.local/bin/claude"))),
            ("CLAUDE_CONFIG_DIR",
             expand(values.get("CLAUDE_CONFIG_DIR", "~/.claude"))),
            ("CLAUDE_CREDENTIALS_FILE",
             expand(values.get("CLAUDE_CREDENTIALS_FILE", "~/.claude.json"))),
            ("CODEX_CONFIG_DIR",
             expand(values.get("CODEX_CONFIG_DIR", "~/.codex"))),
            ("CONTAINER_HOME",
             values.get("CONTAINER_HOME", "/home/appuser")),
        ]),
    ]

    lines: list[str] = []
    for section_name, pairs in sections:
        lines.append(f"# ─── {section_name} {'─' * max(0, 44 - len(section_name))}")
        for key, value in pairs:
            lines.append(f"{key}={value}")
        lines.append("")

    path.write_text("\n".join(lines))


# ── Step 1: Environment configuration ──────────────────────────────────────

def setup_env() -> dict[str, str]:
    """Interactively collect .env values. Returns the final values dict."""
    print_step(1, "Environment Configuration (.env)")

    home = Path.home()
    env_path = Path(".env")
    existing: dict[str, str] = {}
    merge_mode = False

    if env_path.exists():
        choice = input(
            f"\n  {yellow('.env already exists.')} "
            "[O]verwrite / [M]erge / [C]ancel (o/m/c)? "
        ).strip().lower()
        if choice == "c" or not choice:
            print(f"  {yellow('Skipping .env setup.')}")
            return read_existing_env(env_path)
        if choice == "m":
            merge_mode = True
            existing = read_existing_env(env_path)
            print(f"  {green('Merge mode:')} existing values kept, only missing ones prompted.\n")
        # else: overwrite — proceed with empty existing

    def get(key: str, label: str, default: str = "",
            secret: bool = False, autogen: bool = False) -> str:
        if merge_mode and key in existing:
            return existing[key]
        hint = default
        if autogen and not hint:
            hint = "<auto-generate>"
        value = prompt(label, default=hint or None, secret=secret)
        if autogen and (not value or value == "<auto-generate>"):
            generated = secrets.token_hex(32)
            print(f"    {green('→')} Generated: {generated[:8]}...{generated[-8:]}")
            return generated
        return value

    values: dict[str, str] = {}

    # ── Database ──
    print(bold("  [Database]"))
    values["DB_USER"] = get("DB_USER", "DB user", default="customclaw")
    values["DB_PASSWORD"] = get(
        "DB_PASSWORD", "DB password", default="changeme", secret=True
    )

    # ── OpenSearch ──
    print(bold("\n  [OpenSearch]"))
    values["OPENSEARCH_ADMIN_PASSWORD"] = get(
        "OPENSEARCH_ADMIN_PASSWORD", "OpenSearch admin password",
        default="changeme", secret=True
    )

    # ── LLM Providers ──
    print(bold("\n  [LLM Providers]"))
    values["ANTHROPIC_API_KEY"] = get(
        "ANTHROPIC_API_KEY", "Anthropic API key (sk-ant-...)", secret=True
    )
    values["VOYAGE_API_KEY"] = get(
        "VOYAGE_API_KEY", "Voyage API key (optional)", secret=True
    )

    # ── Slack ──
    print(bold("\n  [Slack]"))
    app_token = get(
        "CUSTOMCLAW_SLACK_APP_TOKEN",
        "Slack app token (xapp-...)",
        default="xapp-...",
        secret=True,
    )
    if app_token and not app_token.startswith("xapp-"):
        print(f"    {yellow('Warning:')} app token should start with xapp-")
    values["CUSTOMCLAW_SLACK_APP_TOKEN"] = app_token

    bot_token = get(
        "CUSTOMCLAW_SLACK_BOT_TOKEN",
        "Slack bot token (xoxb-...)",
        default="xoxb-...",
        secret=True,
    )
    if bot_token and not bot_token.startswith("xoxb-"):
        print(f"    {yellow('Warning:')} bot token should start with xoxb-")
    values["CUSTOMCLAW_SLACK_BOT_TOKEN"] = bot_token

    # ── GitHub ──
    print(bold("\n  [GitHub]"))
    values["GITHUB_TOKEN"] = get("GITHUB_TOKEN", "GitHub token (ghp_...)", secret=True)
    values["GITHUB_CLIENT_ID"] = get("GITHUB_CLIENT_ID", "GitHub OAuth client ID")
    values["GITHUB_CLIENT_SECRET"] = get(
        "GITHUB_CLIENT_SECRET", "GitHub OAuth client secret", secret=True
    )

    # ── Airflow ──
    print(bold("\n  [Airflow]"))
    values["AIRFLOW_SECRET_KEY"] = get(
        "AIRFLOW_SECRET_KEY", "Airflow secret key (Enter to auto-generate)",
        secret=True, autogen=True
    )
    values["AIRFLOW_API_USER"] = get("AIRFLOW_API_USER", "Airflow API user", default="admin")
    values["AIRFLOW_API_PASSWORD"] = get(
        "AIRFLOW_API_PASSWORD", "Airflow API password", default="changeme", secret=True
    )

    # ── Web UI ──
    print(bold("\n  [Web UI]"))
    values["NEXTAUTH_URL"] = get(
        "NEXTAUTH_URL", "NextAuth URL", default="http://localhost:3000"
    )
    values["NEXTAUTH_SECRET"] = get(
        "NEXTAUTH_SECRET", "NextAuth secret (Enter to auto-generate)",
        secret=True, autogen=True
    )

    # ── Security ──
    print(bold("\n  [Security]"))
    values["ENCRYPTION_KEY"] = get(
        "ENCRYPTION_KEY", "Encryption key (Enter to auto-generate)",
        secret=True, autogen=True
    )

    # ── Host Paths ──
    print(bold("\n  [Host Paths]"))

    workspace_default = str(home / "workspace")
    workspace = get("HOST_WORKSPACE_PATH", "Workspace path", default=workspace_default)
    workspace_path = Path(workspace).expanduser()
    if not workspace_path.exists():
        print(f"    {yellow('Warning:')} path does not exist: {workspace_path}")
    values["HOST_WORKSPACE_PATH"] = workspace

    claude_bin_default = str(home / ".local/bin/claude")
    claude_bin = get("CLAUDE_CLI_BINARY", "Claude CLI binary", default=claude_bin_default)
    if not Path(claude_bin).expanduser().exists():
        print(f"    {yellow('Warning:')} binary not found: {claude_bin}")
    values["CLAUDE_CLI_BINARY"] = claude_bin

    values["CLAUDE_CONFIG_DIR"] = get(
        "CLAUDE_CONFIG_DIR", "Claude config dir", default=str(home / ".claude")
    )
    values["CLAUDE_CREDENTIALS_FILE"] = get(
        "CLAUDE_CREDENTIALS_FILE", "Claude credentials file",
        default=str(home / ".claude.json")
    )
    values["CODEX_CONFIG_DIR"] = get(
        "CODEX_CONFIG_DIR", "Codex config dir", default=str(home / ".codex")
    )
    values["CONTAINER_HOME"] = get(
        "CONTAINER_HOME", "Container home dir", default="/home/appuser"
    )

    write_env(env_path, values)
    print(f"\n  {green('✓')} .env written to {env_path.resolve()}")
    return values


# ── Step 2: Bot creation ────────────────────────────────────────────────────

_BOT_YAML_TEMPLATE = """\
name: {name}
slack:
  app_token: ${{CUSTOMCLAW_SLACK_APP_TOKEN}}
  bot_token: ${{CUSTOMCLAW_SLACK_BOT_TOKEN}}
  channels: []
persona:
  display_name: "{display_name}"
  description: "{personality}"
  personality: |
    {personality}
project:
  repo_path: ""
  github_repo: ""
  github_token_var: GITHUB_TOKEN
airflow:
  dag_prefix: ""
claude:
  provider: {provider}
  model: {model}
  max_turns: 10
  full_agent: {full_agent}
memory:
  context_window: 20
  auto_extract: true
tools:
  enabled:
    - create_bot
    - list_bots
    - update_bot
    - delete_bot
    - get_dag_status
    - list_dags
    - list_dag_runs
    - trigger_dag
security:
  allowed_channels: []
  allowed_users: []
  dangerous_tools:
    - delete_bot
    - trigger_dag
"""


def create_bot(env_values: dict[str, str]) -> None:
    """Interactively create a bot YAML file under bots/."""
    print_step(2, "First Bot Creation")

    if not prompt_yn("Create your first bot?", default=True):
        print(f"  {yellow('Skipping bot creation.')}")
        return

    name = prompt("Bot name (kebab-case)", default="my-bot")
    name = name.lower().replace(" ", "-")

    display_name = prompt("Display name", default=name.replace("-", " ").title())
    personality = prompt("Personality (one-liner)", default="A helpful assistant")

    print("  LLM provider:")
    print("    1) claude (default)")
    print("    2) codex")
    provider_choice = input("  Choice [1]: ").strip()
    provider = "codex" if provider_choice == "2" else "claude"

    print("  Model:")
    print("    1) sonnet (default)")
    print("    2) opus")
    print("    3) haiku")
    model_choice = input("  Choice [1]: ").strip()
    model_map = {"2": "opus", "3": "haiku"}
    model = model_map.get(model_choice, "sonnet")

    full_agent = prompt_yn("Enable full agent mode?", default=False)

    bots_dir = Path("bots")
    bots_dir.mkdir(exist_ok=True)

    dags_dir = Path("dags")
    dags_dir.mkdir(exist_ok=True)

    bot_path = bots_dir / f"{name}.yaml"
    bot_path.write_text(
        _BOT_YAML_TEMPLATE.format(
            name=name,
            display_name=display_name,
            personality=personality,
            provider=provider,
            model=model,
            full_agent=str(full_agent).lower(),
        )
    )

    print(f"\n  {green('✓')} Bot created: {bot_path.resolve()}")
    print(f"  {green('✓')} dags/ directory ready: {dags_dir.resolve()}")


# ── Step 3: Launch ──────────────────────────────────────────────────────────

def launch_services() -> None:
    """Optionally run docker compose up -d."""
    print_step(3, "Launch Services")

    if not prompt_yn("Run 'docker compose up -d' now?", default=True):
        print(f"  {yellow('Skipping launch.')}")
        return

    print(f"\n  {cyan('Running:')} docker compose up -d\n")
    result = subprocess.run(["docker", "compose", "up", "-d"])
    if result.returncode == 0:
        print(f"\n  {green('✓')} Services started successfully.")
    else:
        print(f"\n  {yellow('⚠')} docker compose exited with code {result.returncode}.")
        print("  Check the output above for errors.")


# ── Summary ─────────────────────────────────────────────────────────────────

def print_summary(created: list[str]) -> None:
    print(f"\n{bold(green('══ Setup complete ══'))}\n")
    if created:
        print(bold("  Created:"))
        for item in created:
            print(f"    {green('✓')} {item}")
    print(f"\n{bold('  Next steps:')}")
    print(f"    • Review {cyan('.env')} and adjust any values")
    print(f"    • Add bots to {cyan('bots/')} (see bots/*.yaml)")
    print(f"    • Start services: {cyan('docker compose up -d')}")
    print(f"    • View logs:      {cyan('docker compose logs -f')}\n")


# ── Entry point ─────────────────────────────────────────────────────────────

def main() -> None:
    print(f"\n{bold(cyan('CustomClaw Setup'))}")
    print("Sets up your .env, creates a bot, and optionally starts services.")

    created: list[str] = []

    try:
        env_values = setup_env()
        env_path = Path(".env")
        if env_path.exists():
            created.append(str(env_path.resolve()))

        create_bot(env_values)
        bots_dir = Path("bots")
        if bots_dir.exists():
            yaml_files = list(bots_dir.glob("*.yaml"))
            for f in yaml_files:
                item = str(f.resolve())
                if item not in created:
                    created.append(item)

        launch_services()

    except KeyboardInterrupt:
        print("\nSetup cancelled.")
        sys.exit(1)

    print_summary(created)


if __name__ == "__main__":
    main()
