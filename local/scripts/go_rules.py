#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Go vet/skip rules from local/upstream-sync.toml, shared by `sync_upstream.py check` and local-ci.yml.

CLI: go_rules.py services/<mod>  ->  shell lines `GO_VET=0|1` and `GO_SKIP='<regex>'` (for eval).
"""
import shlex
import sys
import tomllib
from pathlib import Path

TOML = Path(__file__).resolve().parents[1] / "upstream-sync.toml"


def rules(cfg, mod):
    return mod not in cfg.get("go_vet_skip", []), cfg.get("go_skip", {}).get(mod, "")


if __name__ == "__main__":
    vet, skip = rules(tomllib.loads(TOML.read_text()), sys.argv[1])
    print(f"GO_VET={int(vet)}")
    print(f"GO_SKIP={shlex.quote(skip)}")
