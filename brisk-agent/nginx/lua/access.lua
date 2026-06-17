-- access_by_lua_file (Phase 4 Step 6 Part 5): errors-only rate limiting — reject
-- (429) when this client IP is already over a login/OTP error-rate limit. No-op
-- for zones without errors-only limits. Runs before proxying.
require("brisk").access()
