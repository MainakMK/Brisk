-- init_by_lua_file (Phase 4 Step 5): runs once at master start and on every reload.
-- Loads the FFI fast path (resty.core) + the agent-rendered per-zone data. nginx -t
-- executes this too, so a data/syntax error is caught before the reload (rollback).
require "resty.core"
require("brisk").load("/etc/brisk/lua/zones_data.lua")
