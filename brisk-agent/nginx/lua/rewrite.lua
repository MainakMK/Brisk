-- rewrite_by_lua_file (Phase 4 Step 5): per-zone cache rules (redirect/bypass/mark)
-- + request header transforms. Runs before WAF/access + cache lookup.
require("brisk").rewrite()
