#!/usr/bin/env python3
"""Refresh the model catalog of every provider configured in Crush.

Started from ~/.config/crush/crushrc in the background on every Crush start.
For each managed provider it fetches the live model list, fills in the metadata
Crush needs to behave correctly (context window, max output, pricing, vision,
and reasoning effort levels), and writes the result to the machine-owned data
config (~/.local/share/crush/crush.json, key providers.<id>.models).

It runs detached (setsid) so startup never waits on the network: a session
uses the catalog the previous refresh wrote, and the next start sees the one
this refresh produces. Arrays are concatenated across config files rather than
replaced, so exactly one file may declare providers.<id>.models for a managed
provider; the curated knowledge this script builds on lives in
model-sync-seed.json instead.

Reasoning efforts are resolved in this order (first hit wins):
  1. model-sync-overrides.json pin (per provider id or "*")
  2. the provider's own /models metadata (authoritative: a provider that
     says a model has no adjustable efforts is believed)
  3. the curated entry for the same id in model-sync-seed.json
  4. levels advertised for the same model family by another provider
  5. the same family in model-sync-seed.json
Steps 4 and 5 only run for providers that publish no reasoning metadata at
all (pgsgrove, Command Code): inventing an effort for a provider that does
advertise would get the request rejected upstream.
  6. the provider's own "can reason" capability flag (levels unknown -> let the
     server decide, which is what Crush does for models without levels)
  7. nothing (can_reason false)

Family matching normalizes ids (vendor prefix and . / _ / : decorations
removed) and then walks truncated parents, so "claude-opus-4.8" from one
provider matches "claude-opus-4-8" from another.

Secrets are read from the environment (crushrc exports them) and, for Copilot,
from the OAuth token Crush already stores in the data config. A failure never
breaks startup: when a provider cannot be reached the previous catalog (data
config, then cache) is kept, and the exit status stays 0.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import contextlib
import fcntl
import json
import os
import re
import shutil
import sys
import tempfile
import time
import traceback
import urllib.error
import urllib.request
from pathlib import Path

HOME = Path.home()
APP = "crush"

# This runs in the background (see crushrc), so the timeouts only bound how
# long a detached refresh may live; nothing waits on it.
REQUEST_TIMEOUT = float(os.environ.get("CRUSH_MODEL_SYNC_TIMEOUT", "8"))
TOTAL_TIMEOUT = float(os.environ.get("CRUSH_MODEL_SYNC_BUDGET", "15"))
# Seconds a generated catalog stays fresh. Crush spawns itself often (sub-agent
# runs), so a short window keeps back-to-back starts from re-fetching every
# provider. 0 forces a network refresh on every start.
CACHE_TTL = float(os.environ.get("CRUSH_MODEL_SYNC_TTL", "120"))

BROWSER_UA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"

LEVEL_ORDER = ["none", "minimal", "low", "medium", "high", "xhigh", "max"]

# Copilot models that only accept /responses and are missing from Crush's
# compiled-in routing map. They 400 through /chat/completions, so they are
# dropped unless the installed binary carries the responses-models patch and
# the id is listed in copilot-responses-models.
COPILOT_DENYLIST = {"mai-code-1.1-flash"}
COPILOT_RESPONSES_FILE = "copilot-responses-models"

# Mistral does not advertise reasoning efforts; these come from
# docs.mistral.ai/studio/conversations/reasoning. Keyed by normalized id
# prefix (see norm_key). Models that reason natively (magistral, devstral)
# reject reasoning_effort with a 422, so they get no levels.
MISTRAL_LEVEL_RULES = [
    ("mistral-small", (["none", "high"], "high")),
    ("mistral-medium", (["none", "high"], "high")),
    ("mistral-large", (["none", "high"], "high")),
    ("glm-", (["low", "high", "max"], "high")),
    ("zai-glm", (["low", "high", "max"], "high")),
    # Mistral-hosted third-party DeepSeek V4.1 Flash 1017. The model rejects
    # medium and minimal with a 400 naming none/low/high/xhigh/max, so the
    # picker offers only those.
    ("deepseek-v4-1-flash", (["none", "low", "high", "xhigh", "max"], "max")),
]

# Brand casing for generated display names.
BRANDS = {
    "glm": "GLM", "gpt": "GPT", "vl": "VL", "ai": "AI", "llm": "LLM",
    "deepseek": "DeepSeek", "qwen": "Qwen", "kimi": "Kimi", "mimo": "MiMo",
    "minimax": "MiniMax", "gemma": "Gemma", "nemotron": "Nemotron",
    "ling": "Ling", "ornith": "Ornith", "muse": "Muse", "beam": "Beam",
    "claude": "Claude", "gemini": "Gemini", "mistral": "Mistral",
    "codestral": "Codestral", "magistral": "Magistral", "devstral": "Devstral",
    "pixtral": "Pixtral", "ministral": "Ministral", "grok": "Grok",
    "llama": "Llama", "hermes": "Hermes", "step": "Step", "hy": "HY",
    "inkling": "Inkling", "longcat": "LongCat", "laguna": "Laguna",
    "fugu": "Fugu", "glyph": "Glyph", "aya": "Aya", "cohere": "Cohere",
    "command": "Command", "nvidia": "NVIDIA", "tencent": "Tencent",
    "zai": "Z.AI", "xai": "xAI", "nemotronh": "NemotronH", "m2": "M2",
    "fp8": "FP8", "gb": "GB", "b": "B",
}


# --------------------------------------------------------------------------
# paths


def _config_home() -> Path:
    return Path(os.environ.get("XDG_CONFIG_HOME") or (HOME / ".config"))


def config_dir() -> Path:
    override = os.environ.get("CRUSH_GLOBAL_CONFIG")
    return Path(override) if override else _config_home() / APP


def data_config_path() -> Path:
    override = os.environ.get("CRUSH_GLOBAL_DATA")
    if override:
        return Path(override) / f"{APP}.json"
    xdg = os.environ.get("XDG_DATA_HOME")
    if xdg:
        return Path(xdg) / APP / f"{APP}.json"
    return HOME / ".local" / "share" / APP / f"{APP}.json"


def cache_dir() -> Path:
    override = os.environ.get("CRUSH_CACHE_DIR")
    if override:
        return Path(override)
    xdg = os.environ.get("XDG_CACHE_HOME")
    if xdg:
        return Path(xdg) / APP
    return HOME / ".cache" / APP


def seed_path() -> Path:
    return config_dir() / "model-sync-seed.json"


def overrides_path() -> Path:
    return config_dir() / "model-sync-overrides.json"


def cache_path() -> Path:
    return cache_dir() / "model-sync-cache.json"


def log_path() -> Path:
    return cache_dir() / "model-sync.log"


# --------------------------------------------------------------------------
# small helpers


def read_json(path: Path, default):
    try:
        with open(path, "r", encoding="utf-8") as fh:
            return json.load(fh)
    except FileNotFoundError:
        return default
    except (json.JSONDecodeError, OSError):
        return default


def write_json_atomic(path: Path, payload) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(dir=str(path.parent), prefix=f".{path.name}.", suffix=".tmp")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as fh:
            json.dump(payload, fh, indent=2, sort_keys=False)
            fh.write("\n")
        os.chmod(tmp, 0o600)
        os.replace(tmp, path)
    except BaseException:
        try:
            os.unlink(tmp)
        except OSError:
            pass
        raise


def http_json(url: str, headers: dict[str, str], timeout: float):
    req = urllib.request.Request(url, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return json.load(resp)
    except urllib.error.HTTPError as exc:
        detail = ""
        try:
            detail = exc.read()[:200].decode("utf-8", "replace")
        except Exception:
            pass
        raise RuntimeError(f"HTTP {exc.code} {exc.reason} {detail}".strip()) from exc


def bearer(key: str | None) -> dict[str, str]:
    return {"Authorization": f"Bearer {key}"} if key else {}


def norm_key(model_id: str) -> str:
    """Normalize a model id into a comparison key.

    Vendor namespaces, ":free"-style tags and . / _ decorations are dropped so
    the same model listed by different aggregators compares equal.
    """
    s = model_id.rsplit("/", 1)[-1].lower()
    s = s.split(":", 1)[0]
    s = re.sub(r"[._]", "-", s)
    s = re.sub(r"-{2,}", "-", s).strip("-")
    return s


def sanitize_levels(levels) -> list[str]:
    """Levels that are safe to infer across providers.

    Aggregators advertise provider-specific knobs ("adaptive"/"disabled" for
    MiniMax, "on"/"off" for Nemotron) that only mean something to that
    endpoint, so they never travel to another provider's models. A provider's
    own advertised values are kept as-is by order_levels.
    """
    if not levels:
        return []
    return [v for v in LEVEL_ORDER if v in levels]


def order_levels(levels) -> list[str]:
    """Deduplicate and order levels; unknown provider-specific values last."""
    if not levels:
        return []
    uniq = list(dict.fromkeys(levels))
    known = sorted([v for v in uniq if v in LEVEL_ORDER], key=LEVEL_ORDER.index)
    unknown = sorted([v for v in uniq if v not in LEVEL_ORDER])
    return known + unknown


def level_key(levels) -> tuple[str, ...]:
    if not levels:
        return ()
    return tuple(sorted(set(levels), key=lambda v: LEVEL_ORDER.index(v) if v in LEVEL_ORDER else 99))


def pick_default(levels, preferred: str | None = None) -> str | None:
    if not levels:
        return None
    if preferred and preferred in levels:
        return preferred
    for candidate in ("medium", "high", "low", "max", "xhigh", "minimal", "none"):
        if candidate in levels:
            return candidate
    return levels[0]


def effort_display(value: str) -> str:
    if not value:
        return ""
    if value == "xhigh":
        return "X-High"
    return value[:1].upper() + value[1:]


def pretty_name(model_id: str) -> str:
    base = model_id.rsplit("/", 1)[-1]
    base = base.split(":", 1)[0]
    words = re.split(r"[-_\s]+", base)
    out = []
    for word in words:
        if not word:
            continue
        low = word.lower()
        if low in BRANDS:
            out.append(BRANDS[low])
        elif re.fullmatch(r"\d+(?:\.\d+)?", word):
            out.append(word)
        else:
            out.append(word[0].upper() + word[1:])
    return " ".join(out) if out else model_id


# --------------------------------------------------------------------------
# reasoning knowledge indexes


def _canonical_levels_sets(entries):
    """Group index entries and return (levels, default, can_reason, vision).

    entries: list of (source, levels, default, can_reason, vision). Sources that
    disagree on the level set cancel out: guessing wrong means sending an
    unsupported reasoning_effort, which the upstream rejects with a 400.
    """
    with_levels = [e for e in entries if e[1]]
    if not with_levels:
        if any(e[3] for e in entries):
            return (), None, True, any(e[4] for e in entries)
        return None
    sets = {level_key(e[1]) for e in with_levels}
    if len(sets) != 1:
        return None
    levels = list(sets.pop())
    defaults = [e[2] for e in with_levels if e[2]]
    default = pick_default(levels, defaults[0] if defaults else None)
    return levels, default, True, any(e[4] for e in entries)


class FamilyIndex:
    """Family -> reasoning metadata derived from one or more catalog sources."""

    def __init__(self):
        self.entries: dict[str, list[tuple]] = {}

    def add(self, model_id, source, levels, default, can_reason, vision):
        key = norm_key(model_id)
        if not key:
            return
        clean = level_key(sanitize_levels(levels))
        if default not in clean:
            default = None
        self.entries.setdefault(key, []).append(
            (source, clean, default, bool(can_reason), bool(vision) if vision is not None else False)
        )

    def lookup(self, model_id: str):
        """Resolve family metadata by walking truncated parent ids."""
        parts = norm_key(model_id).split("-")
        candidates = ["-".join(parts[:i]) for i in range(len(parts), 0, -1)]

        for candidate in candidates:
            entries = self.entries.get(candidate)
            if not entries:
                continue
            resolved = _canonical_levels_sets(entries)
            if resolved is not None:
                return resolved

        # Children answer a coarser id: a curated "mistral-large-4-0"
        # resolves "mistral-large-4". Every child must agree.
        for candidate in candidates:
            if len(candidate) < 4:
                continue
            children = [
                entries
                for key, entries in self.entries.items()
                if key.startswith(candidate + "-")
            ]
            if not children:
                continue
            resolved = _canonical_levels_sets([e for entries in children for e in entries])
            if resolved is not None:
                return resolved
        return None


# --------------------------------------------------------------------------
# provider adapters. Each returns a list of partially-normalized dicts:
#   id, name, context_window, default_max_tokens, cost_* , levels (or None),
#   default_effort (or None), can_reason (or None), images (or None)


def _prices(inp=None, out=None, cache_in=None, cache_out=None) -> dict:
    return {
        "cost_per_1m_in": inp,
        "cost_per_1m_out": out,
        "cost_per_1m_in_cached": cache_in,
        "cost_per_1m_out_cached": cache_out,
    }


def parse_neuralwatt(data) -> list[dict]:
    out = []
    for m in data.get("data") or []:
        meta = m.get("metadata") or {}
        caps = meta.get("capabilities") or {}
        reasoning = meta.get("reasoning") or {}
        limits = meta.get("limits") or {}
        pricing = meta.get("pricing") or {}
        out.append(
            {
                "id": m["id"],
                "name": meta.get("display_name") or m["id"],
                "context_window": limits.get("max_context_length") or m.get("max_model_len"),
                "default_max_tokens": limits.get("max_output_tokens"),
                **_prices(
                    pricing.get("input_per_million"),
                    pricing.get("output_per_million"),
                    pricing.get("cached_input_per_million"),
                    pricing.get("cached_output_per_million"),
                ),
                "levels": reasoning.get("supported_efforts") or ([] if caps.get("reasoning") else None),
                "default_effort": reasoning.get("default_effort"),
                "can_reason": caps.get("reasoning"),
                "images": caps.get("vision"),
            }
        )
    return out


def parse_pgsgrove(data) -> list[dict]:
    out = []
    for m in data.get("data") or []:
        if (m.get("type") or "chat") != "chat":
            continue
        pricing = m.get("pricing") or {}
        modalities = m.get("modalities") or {}
        inputs = modalities.get("input") or []
        out.append(
            {
                "id": m["id"],
                "name": m.get("name") or None,
                "context_window": m.get("context_window"),
                "default_max_tokens": m.get("max_output_tokens"),
                **_prices(
                    pricing.get("input_per_m"),
                    pricing.get("output_per_m"),
                    pricing.get("cached_input_per_m"),
                    pricing.get("cached_output_per_m"),
                ),
                "levels": None,
                "default_effort": None,
                "can_reason": None,
                # Empty modality lists mean "not advertised", not "no images".
                "images": True if "image" in inputs else None,
            }
        )
    return out


def parse_xkiro(data) -> list[dict]:
    out = []
    for m in data.get("data") or []:
        if (m.get("type") or "model") not in ("model", "chat"):
            continue
        if (m.get("modality") or "chat") != "chat":
            continue
        caps = m.get("capabilities") or {}
        pricing = m.get("pricing") or {}
        efforts = m.get("reasoning_efforts") or {}
        levels = efforts.get("levels") or ([] if caps.get("reasoning") else None)
        out.append(
            {
                "id": m["id"],
                "name": m.get("display_name") or m["id"],
                "context_window": m.get("context_length"),
                "default_max_tokens": m.get("max_output_tokens"),
                **_prices(
                    pricing.get("input"),
                    pricing.get("output"),
                    pricing.get("cache_read"),
                    pricing.get("cache_write"),
                ),
                "levels": levels,
                "default_effort": efforts.get("default"),
                "can_reason": caps.get("reasoning"),
                "images": caps.get("vision"),
            }
        )
    return out


def parse_commandcode(data, endpoint: str) -> list[dict]:
    out = []
    for m in data.get("data") or []:
        endpoints = m.get("supported_endpoints") or []
        if endpoint not in endpoints:
            continue
        out.append(
            {
                "id": m["id"],
                "name": m.get("name") or None,
                "context_window": m.get("context_length"),
                "default_max_tokens": None,
                **_prices(),
                "levels": None,
                "default_effort": None,
                "can_reason": None,
                "images": None,
            }
        )
    return out


def parse_mistral(data) -> list[dict]:
    out = []
    for m in data.get("data") or []:
        caps = m.get("capabilities") or {}
        if not caps.get("completion_chat"):
            continue
        reasoning = caps.get("reasoning")
        levels, default = [], None
        key = norm_key(m["id"])
        for prefix, (rule_levels, rule_default) in MISTRAL_LEVEL_RULES:
            if key.startswith(prefix) and reasoning:
                levels, default = list(rule_levels), rule_default
                break
        out.append(
            {
                "id": m["id"],
                "name": m.get("name") or m["id"].rsplit("/", 1)[-1],
                "context_window": m.get("max_context_length"),
                "default_max_tokens": None,
                **_prices(),
                "levels": levels if reasoning else None,
                "default_effort": default,
                "can_reason": reasoning,
                "images": caps.get("vision"),
            }
        )
    return out


def parse_copilot(data, allow_responses_ids: set[str]) -> list[dict]:
    out = []
    for m in data.get("data") or []:
        caps = m.get("capabilities") or {}
        if caps.get("type") != "chat":
            continue
        if (m.get("policy") or {}).get("state") not in (None, "enabled"):
            continue
        if m.get("model_picker_enabled") is False:
            continue
        mid = m["id"]
        if mid in COPILOT_DENYLIST and mid not in allow_responses_ids:
            continue
        supports = caps.get("supports") or {}
        limits = caps.get("limits") or {}
        levels = supports.get("reasoning_effort") or []
        # Copilot serves some models only through the Responses API. Sending
        # one to /chat/completions fails with "not accessible via the
        # /chat/completions endpoint", so record the wire type the API
        # advertises and let Crush route the model accordingly. This mirrors
        # catwalk's own Copilot generator, which prefers /responses whenever
        # the model advertises it.
        endpoints = m.get("supported_endpoints") or []
        wire_type = "responses" if "/responses" in endpoints else None
        out.append(
            {
                "id": mid,
                "name": m.get("name") or mid,
                "context_window": limits.get("max_context_window_tokens"),
                "default_max_tokens": limits.get("max_output_tokens"),
                **_prices(),
                "levels": levels,
                "default_effort": None,
                "can_reason": supports.get("adaptive_thinking", bool(levels)),
                "images": supports.get("vision"),
                "wire_type": wire_type,
            }
        )
    return out


# --------------------------------------------------------------------------
# provider registry


class Provider:
    """One provider's model endpoint and the parser for its payload."""

    def __init__(self, pid, url, *, env=None, parse=None, ua=None,
                 extra_headers=None, urls=None, copilot=False, cross_fill=False):
        self.id = pid
        self.url = url
        self.urls = urls or [url]
        self.env = env
        self.parse = parse
        self.ua = ua
        self.extra_headers = extra_headers or {}
        self.copilot = copilot
        # Only providers that publish no reasoning metadata borrow another
        # provider's levels for the same model family. Providers that do
        # advertise are taken literally: adding an effort they did not list
        # would make the upstream reject the request.
        self.cross_fill = cross_fill

    def fetch(self, copilot_token=None, timeout=None):
        timeout = timeout or REQUEST_TIMEOUT
        last_error = None
        for url in self.urls:
            try:
                if self.copilot:
                    if not copilot_token:
                        raise RuntimeError("no Copilot token in the data config")
                    headers = {
                        "Accept": "application/json",
                        **COPILOT_HEADERS,
                        **bearer(copilot_token),
                    }
                    payload = http_json(url, headers, timeout)
                    tier_payload = http_json(
                        url, {**headers, "X-GitHub-Api-Version": "2026-06-01"}, timeout
                    )
                    models = self.parse(payload, copilot_allowed_responses_ids())
                    tiers = {model["id"]: model.get("billing") or {} for model in tier_payload["data"]}
                    variants = []
                    for model in models:
                        prices = tiers.get(model["id"], {}).get("token_prices") or {}
                        window = (prices.get("long_context") or {}).get("context_max")
                        if type(window) is not int or window <= (model["context_window"] or 0):
                            continue
                        label = f"{window / 1_000_000:g}M" if window >= 1_000_000 else f"{window // 1_000}K"
                        variants.append({
                            **model,
                            "id": model["id"] + "[long]",
                            "name": f"{model['name']} ({label} context)",
                            "context_window": window,
                        })
                    return models + variants

                key = os.environ.get(self.env) if self.env else None
                if self.env and not key:
                    raise RuntimeError(f"{self.env} is not set")
                headers = {"Accept": "application/json"}
                if self.ua:
                    headers["User-Agent"] = self.ua
                headers.update(bearer(key))
                headers.update(self.extra_headers)
                payload = http_json(url, headers, timeout)
                return self.parse(payload)
            except Exception as exc:  # noqa: BLE001 - reported per provider
                last_error = exc
        raise RuntimeError(str(last_error))


COMMANDCODE_HEADERS = {"User-Agent": "commandcode-cli", "x-cmd-zdr": "1"}

PROVIDERS = [
    Provider(
        "neuralwatt",
        "https://api.neuralwatt.com/v1/models",
        env="NEURALWATT_API_KEY",
        parse=parse_neuralwatt,
    ),
    Provider(
        "pgsgrove",
        "https://api.pgsgrove.com/v1/models",
        env="PGSCROVE_API_KEY",
        ua=BROWSER_UA,
        parse=parse_pgsgrove,
        cross_fill=True,
    ),
    Provider(
        "xkiro",
        "https://api.xkiro.com/v1/models",
        env="XKIRO_API_KEY",
        ua=BROWSER_UA,
        parse=parse_xkiro,
    ),
    Provider(
        "commandcode",
        "https://api.commandcode.ai/provider/v1/models",
        env="COMMANDCODE_API_KEY",
        extra_headers=COMMANDCODE_HEADERS,
        parse=lambda data: parse_commandcode(data, "/chat/completions"),
        cross_fill=True,
    ),
    Provider(
        "commandcode-claude",
        "https://api.commandcode.ai/provider/v1/models",
        env="COMMANDCODE_API_KEY",
        extra_headers=COMMANDCODE_HEADERS,
        parse=lambda data: parse_commandcode(data, "/messages"),
        cross_fill=True,
    ),
    Provider(
        "mistral",
        "https://api.mistral.ai/v1/models",
        env="MISTRAL_API_KEY",
        parse=parse_mistral,
    ),
    Provider(
        "copilot",
        "https://api.business.githubcopilot.com/models",
        urls=[
            "https://api.business.githubcopilot.com/models",
            "https://api.githubcopilot.com/models",
        ],
        parse=parse_copilot,
        copilot=True,
    ),
]

COPILOT_HEADERS = {
    "User-Agent": "GitHubCopilotChat/0.35.0",
    "Editor-Version": "vscode/1.107.0",
    "Editor-Plugin-Version": "copilot-chat/0.35.0",
    "Copilot-Integration-Id": "vscode-chat",
}


# --------------------------------------------------------------------------
# copilot credentials


def copilot_allowed_responses_ids() -> set[str]:
    """Ids listed in copilot-responses-models, only when the binary routes them."""
    path = config_dir() / COPILOT_RESPONSES_FILE
    try:
        text = path.read_text(encoding="utf-8")
    except OSError:
        return set()
    ids = {
        line.strip()
        for line in text.splitlines()
        if line.strip() and not line.lstrip().startswith("#")
    }
    if not ids:
        return set()
    binary = shutil.which("crush")
    if not binary:
        return set()
    try:
        target = os.path.realpath(binary)
        candidate = Path(os.path.dirname(target)) / "bin" / "crush"
        if candidate.is_file():
            target = str(candidate)
        with open(target, "rb") as fh:
            if b"copilot-responses-models" not in fh.read():
                return set()
    except OSError:
        return set()
    return {part.strip() for i in ids for part in i.split(",") if part.strip()}


def copilot_token() -> str | None:
    """Return a usable Copilot token, refreshing the stored one when needed."""
    cfg = read_json(data_config_path(), {})
    oauth = ((cfg.get("providers") or {}).get("copilot") or {}).get("oauth") or {}
    token = oauth.get("access_token") or ((cfg.get("providers") or {}).get("copilot") or {}).get("api_key")
    expires_at = oauth.get("expires_at") or 0
    if token and (not expires_at or expires_at - 60 > time.time()):
        return token
    github_token = oauth.get("refresh_token")
    if not github_token:
        return token
    headers = {"Accept": "application/json", **COPILOT_HEADERS, **bearer(github_token)}
    try:
        payload = http_json(
            "https://api.github.com/copilot_internal/v2/token", headers, REQUEST_TIMEOUT
        )
        return payload.get("token") or token
    except Exception:
        return token


# --------------------------------------------------------------------------
# building the catalog


def finalize_model(pid, raw, seed_same, adv_index, seed_index, overrides, cross_fill) -> dict:
    mid = raw["id"]
    seeded = seed_same.get(mid) or {}
    pin = (
        (overrides.get(pid) or {}).get(mid)
        or (overrides.get("*") or {}).get(mid)
        or {}
    )

    can_reason = pin.get("can_reason")
    if can_reason is None:
        can_reason = raw.get("can_reason")
    if can_reason is None:
        can_reason = seeded.get("can_reason")

    levels = pin.get("reasoning_levels")
    if levels is None:
        levels = raw.get("levels")
    if levels is None:
        levels = seeded.get("reasoning_levels")
    # The provider's own values are authoritative for its API; only the
    # cross-provider index is restricted to levels Crush understands.
    levels = order_levels(levels)

    # A provider saying "this model cannot reason" is authoritative: never
    # borrow another family member's efforts in that case.
    cross = None
    if cross_fill and can_reason is not False and not levels:
        # A source that knows the model reasons but not which efforts are
        # valid must not shadow a source that does know: prefer any answer
        # that carries levels, and keep the bare "can reason" verdict as a
        # fallback.
        for index in (adv_index, seed_index):
            resolved = index.lookup(mid)
            if resolved and resolved[0]:
                cross = resolved
                break
            if resolved and cross is None:
                cross = resolved
        if cross and cross[0]:
            levels = list(cross[0])

    default_effort = pin.get("default_reasoning_effort") or raw.get("default_effort")
    if not default_effort:
        seeded_default = seeded.get("default_reasoning_effort")
        if seeded_default in levels:
            default_effort = seeded_default
        elif cross:
            default_effort = cross[1]

    if can_reason is None and cross:
        can_reason = cross[2]
    if can_reason is None and levels:
        can_reason = True
    if can_reason is None:
        can_reason = False

    name = next(
        (n for n in (pin.get("name"), raw.get("name"), seeded.get("name")) if n and n != mid),
        None,
    )
    model = {"id": mid, "name": name or pretty_name(mid)}

    wire_type = pin.get("type") or raw.get("wire_type")
    if wire_type:
        model["type"] = wire_type

    context = pin.get("context_window") or raw.get("context_window") or seeded.get("context_window")
    if context:
        model["context_window"] = int(context)

    max_tokens = pin.get("default_max_tokens") or raw.get("default_max_tokens") or seeded.get(
        "default_max_tokens"
    )
    if max_tokens:
        max_tokens = int(max_tokens)
        if context:
            max_tokens = min(max_tokens, int(context))
        model["default_max_tokens"] = max_tokens

    pricing = {}
    for field, key in (
        ("cost_per_1m_in", "input"),
        ("cost_per_1m_out", "output"),
        ("cost_per_1m_in_cached", "cache_create"),
        ("cost_per_1m_out_cached", "cache_hit"),
    ):
        value = pin.get(field)
        if value is None:
            value = raw.get(field)
        if value is None:
            value = seeded.get(field)
        pricing[key] = float(value) if value is not None else 0
    model["pricing"] = pricing

    reasoning = {"thinking": "toggleable" if can_reason else "never"}
    if can_reason and levels:
        reasoning["effort_levels"] = [
            {"value": level, "display": effort_display(level)} for level in levels
        ]
        reasoning["default_effort_level"] = pick_default(levels, default_effort)
    model["reasoning"] = reasoning

    images = pin.get("supports_attachments")
    if images is None:
        images = raw.get("images")
    if images is None:
        images = seeded.get("supports_attachments")
    if images is None and cross:
        images = cross[3]
    model["capabilities"] = {"vision": bool(images)}

    return model


def normalize_model_schema(model: dict) -> dict:
    """Rewrite a catwalk v2 model dict to the v3 shape Crush persists."""
    if "pricing" not in model:
        pricing = {}
        for legacy, key in (
            ("cost_per_1m_in", "input"),
            ("cost_per_1m_out", "output"),
            ("cost_per_1m_in_cached", "cache_create"),
            ("cost_per_1m_out_cached", "cache_hit"),
        ):
            if legacy in model:
                value = model[legacy]
                pricing[key] = float(value) if value is not None else 0
        if pricing:
            model["pricing"] = pricing
    for legacy in ("cost_per_1m_in", "cost_per_1m_out", "cost_per_1m_in_cached", "cost_per_1m_out_cached"):
        model.pop(legacy, None)

    if "reasoning" not in model and any(
        key in model for key in ("can_reason", "reasoning_levels", "default_reasoning_effort")
    ):
        can_reason = bool(model.get("can_reason"))
        levels = model.get("reasoning_levels") or []
        reasoning = {"thinking": "toggleable" if can_reason else "never"}
        if can_reason and levels:
            reasoning["effort_levels"] = [
                {"value": level, "display": effort_display(level)} for level in levels
            ]
            reasoning["default_effort_level"] = pick_default(
                levels, model.get("default_reasoning_effort")
            )
        model["reasoning"] = reasoning
    for legacy in ("can_reason", "reasoning_levels", "default_reasoning_effort"):
        model.pop(legacy, None)

    if "capabilities" not in model and "supports_attachments" in model:
        model["capabilities"] = {"vision": bool(model.get("supports_attachments"))}
    model.pop("supports_attachments", None)
    return model


def build_catalog(pid, raw_models, seed_same, adv_index, seed_index, overrides, cross_fill):
    models = [
        finalize_model(pid, raw, seed_same, adv_index, seed_index, overrides, cross_fill)
        for raw in raw_models
    ]
    models.sort(key=lambda m: m["id"])

    # Aliased ids upstream (Mistral's "-latest" endpoints) report the name of
    # the model they point at, which would label two picker rows identically.
    ids = {m["id"] for m in models}
    for model in models:
        name = model["name"]
        if name in ids and name != model["id"]:
            seeded_name = (seed_same.get(model["id"]) or {}).get("name")
            if seeded_name and seeded_name not in ids:
                model["name"] = seeded_name
            else:
                model["name"] = pretty_name(model["id"])

    counts = {}
    for model in models:
        counts[model["name"]] = counts.get(model["name"], 0) + 1
    for model in models:
        if counts[model["name"]] > 1:
            model["name"] = model["id"]
    return models


def seed_models(seed: dict, pid: str) -> dict:
    provider = (seed.get("providers") or {}).get(pid) or {}
    return {m["id"]: m for m in provider.get("models") or [] if m.get("id")}


def advert_index(catalogs: dict[str, list[dict]]) -> FamilyIndex:
    index = FamilyIndex()
    for pid, models in catalogs.items():
        for model in models:
            if model.get("levels"):
                index.add(
                    model["id"],
                    pid,
                    model["levels"],
                    model.get("default_effort"),
                    model.get("can_reason"),
                    model.get("images"),
                )
    return index


def seed_index(seed: dict, managed: set[str]) -> FamilyIndex:
    index = FamilyIndex()
    for pid, provider in (seed.get("providers") or {}).items():
        if pid not in managed:
            continue
        for model in (provider or {}).get("models") or []:
            if model.get("reasoning_levels"):
                index.add(
                    model["id"],
                    pid,
                    model["reasoning_levels"],
                    model.get("default_reasoning_effort"),
                    model.get("can_reason"),
                    model.get("supports_attachments"),
                )
    return index


# --------------------------------------------------------------------------
# writing


@contextlib.contextmanager
def config_lock(path: Path):
    """Hold Crush's own config lock so a concurrent Crush write cannot race."""
    lock = Path(str(path) + ".lock")
    lock.parent.mkdir(parents=True, exist_ok=True)
    with open(lock, "a+", encoding="utf-8") as fh:
        fcntl.flock(fh.fileno(), fcntl.LOCK_EX)
        try:
            yield
        finally:
            fcntl.flock(fh.fileno(), fcntl.LOCK_UN)


def merge_into_data_config(catalogs: dict[str, list[dict]]) -> bool:
    """Write providers.<id>.models into the data config. Returns True on change."""
    path = data_config_path()
    with config_lock(path):
        return _merge_into_data_config(path, catalogs)


def _merge_into_data_config(path: Path, catalogs: dict[str, list[dict]]) -> bool:
    current = read_json(path, {})
    if not isinstance(current, dict):
        current = {}
    providers = current.get("providers")
    if not isinstance(providers, dict):
        providers = {}
        current["providers"] = providers
    changed = False
    for pid, models in catalogs.items():
        entry = providers.get(pid)
        if not isinstance(entry, dict):
            entry = {}
            providers[pid] = entry
        if entry.get("models") != models:
            entry["models"] = models
            changed = True
    if not changed:
        return False
    write_json_atomic(path, current)
    return True


def load_cache() -> dict:
    cache = read_json(cache_path(), {})
    if not isinstance(cache, dict) or cache.get("schema") != 3:
        return {}
    return cache


def write_cache(
    catalogs: dict[str, list[dict]], failures: dict[str, str], generated_at: float | None = None
) -> None:
    payload = {
        # A cache hit re-publishes the same catalogs; keeping the original
        # timestamp is what lets the TTL actually expire.
        "schema": 3,
        "generated_at": int(generated_at or time.time()),
        "providers": catalogs,
        "failures": failures,
    }
    write_json_atomic(cache_path(), payload)


class Logger:
    def __init__(self, verbose: bool):
        self.lines: list[str] = []
        self.verbose = verbose

    def __call__(self, message: str) -> None:
        self.lines.append(message)
        if self.verbose:
            print(message)

    def flush(self) -> None:
        try:
            path = log_path()
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("\n".join(self.lines) + "\n", encoding="utf-8")
        except OSError:
            pass


def summarize(pid: str, old: list[dict] | None, new: list[dict]) -> str:
    old_ids = {m.get("id") for m in (old or [])}
    new_ids = {m["id"] for m in new}
    added = sorted(new_ids - old_ids)
    removed = sorted(old_ids - new_ids)
    with_reasoning = sum(1 for m in new if (m.get("reasoning") or {}).get("effort_levels"))
    parts = [f"{pid}: {len(new)} models ({with_reasoning} with reasoning efforts)"]
    if added:
        parts.append(f"+{len(added)}: {', '.join(added[:6])}{'…' if len(added) > 6 else ''}")
    if removed:
        parts.append(f"-{len(removed)}: {', '.join(removed[:6])}{'…' if len(removed) > 6 else ''}")
    return "\n  ".join(parts)


def run(args) -> int:
    log = Logger(args.verbose)
    managed = {p.id for p in PROVIDERS}
    seed = read_json(seed_path(), {})
    overrides = read_json(overrides_path(), {})
    if not isinstance(overrides, dict):
        overrides = {}

    previous = read_json(data_config_path(), {})
    if not isinstance(previous, dict):
        previous = {}
    previous_models = {
        pid: ((previous.get("providers") or {}).get(pid) or {}).get("models")
        for pid in managed
    }
    # Compare against what Crush actually showed last start, falling back to
    # the curated seed on a first run.
    baseline = {
        pid: previous_models.get(pid) or list(seed_models(seed, pid).values()) or None
        for pid in managed
    }

    cache = load_cache()
    cached_catalogs = cache.get("providers") if isinstance(cache.get("providers"), dict) else {}
    cache_age = time.time() - float(cache.get("generated_at") or 0)

    catalogs: dict[str, list[dict]] = {}
    failures: dict[str, str] = {}

    from_cache = bool(
        CACHE_TTL > 0 and cache_age < CACHE_TTL and all(cached_catalogs.get(p) for p in managed)
    )
    if from_cache:
        log(f"cache hit ({int(cache_age)}s old, ttl {int(CACHE_TTL)}s)")
        catalogs = {p: cached_catalogs[p] for p in managed}
    else:
        token = copilot_token()
        deadline = time.time() + TOTAL_TIMEOUT
        errors: dict[str, str] = {}

        raw: dict[str, list[dict]] = {}

        def fetch(provider: Provider):
            # Bound each request by what is left of the overall budget so one
            # slow endpoint cannot stretch the refresh far past it.
            remaining = max(deadline - time.time(), 1.0)
            return provider.fetch(token, min(REQUEST_TIMEOUT, remaining))

        pool = concurrent.futures.ThreadPoolExecutor(max_workers=len(PROVIDERS))
        try:
            futures = {pool.submit(fetch, p): p for p in PROVIDERS}
            try:
                for future in concurrent.futures.as_completed(
                    futures, timeout=max(deadline - time.time(), 0.1)
                ):
                    provider = futures[future]
                    try:
                        raw[provider.id] = future.result()
                    except Exception as exc:  # noqa: BLE001
                        errors[provider.id] = str(exc) or exc.__class__.__name__
            except concurrent.futures.TimeoutError:
                pass
            for future, provider in futures.items():
                if provider.id in raw or provider.id in errors:
                    continue
                errors[provider.id] = "timed out"
        finally:
            # Do not wait for stragglers: their sockets expire on their own.
            # (cancel_futures needs Python 3.9; leave unqueued work alone.)
            pool.shutdown(wait=False)

        adv = advert_index(raw)
        seeded_index = seed_index(seed, managed)
        for provider in PROVIDERS:
            pid = provider.id
            if pid in raw and raw[pid]:
                try:
                    catalogs[pid] = build_catalog(
                        pid,
                        raw[pid],
                        seed_models(seed, pid),
                        adv,
                        seeded_index,
                        overrides,
                        provider.cross_fill,
                    )
                except Exception as exc:  # noqa: BLE001 - never lose the others
                    failures[pid] = f"building the catalog failed: {exc}"
                    fallback = cached_catalogs.get(pid) or previous_models.get(pid)
                    log(
                        f"{pid}: building the catalog failed ({exc})"
                        + (" — keeping the previous catalog" if fallback else "")
                    )
                    if fallback:
                        catalogs[pid] = fallback
            else:
                failures[pid] = errors.get(pid, "no models returned")
                # Keep the last good catalog so a network hiccup does not
                # empty the picker.
                fallback = cached_catalogs.get(pid) or previous_models.get(pid)
                log(
                    f"{pid}: live fetch failed ({failures[pid]})"
                    + (" — keeping the previous catalog" if fallback else " — no catalog available")
                )
                if fallback:
                    catalogs[pid] = fallback

    if args.dry_run:
        for provider in PROVIDERS:
            pid = provider.id
            if pid in catalogs:
                log(summarize(pid, baseline.get(pid), catalogs[pid]))
            else:
                log(f"{pid}: skipped ({failures.get(pid, 'unavailable')})")
        log.flush()
        if not args.verbose:
            print("\n".join(log.lines))
        return 0

    for models in catalogs.values():
        for model in models:
            normalize_model_schema(model)
    changed = merge_into_data_config(catalogs)
    write_cache(catalogs, failures, cache.get("generated_at") if from_cache else None)
    for provider in PROVIDERS:
        pid = provider.id
        if pid in catalogs:
            log(summarize(pid, baseline.get(pid), catalogs[pid]))
        else:
            log(f"{pid}: skipped ({failures.get(pid, 'unavailable')})")
    log(f"data config {'updated' if changed else 'unchanged'} ({data_config_path()})")
    log.flush()
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dry-run", action="store_true", help="print what would be written")
    parser.add_argument("--verbose", action="store_true", help="log to stdout")
    parser.add_argument("--force", action="store_true", help="ignore the cache TTL")
    parser.add_argument("--budget", type=float, default=None, help="total fetch budget in seconds")
    parser.add_argument("--timeout", type=float, default=None, help="per-request timeout in seconds")
    args = parser.parse_args()
    if args.force:
        globals()["CACHE_TTL"] = 0.0
    if args.budget is not None:
        globals()["TOTAL_TIMEOUT"] = args.budget
    if args.timeout is not None:
        globals()["REQUEST_TIMEOUT"] = args.timeout
    try:
        return run(args)
    except Exception:  # noqa: BLE001 - startup must never fail because of this
        try:
            path = log_path()
            path.parent.mkdir(parents=True, exist_ok=True)
            with open(path, "a", encoding="utf-8") as fh:
                fh.write("model-sync crashed:\n" + traceback.format_exc())
        except OSError:
            pass
        return 0


if __name__ == "__main__":
    sys.exit(main())
