-- header_filter_by_lua_file (Phase 4 Step 5): override-ttl Cache-Control /
-- force_download (from the cache rule matched in rewrite) + response header
-- transforms. Runs after upstream/cache, before the response goes to the client.
require("brisk").header_filter()
