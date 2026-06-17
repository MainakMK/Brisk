-- log_by_lua_file (Phase 4 Step 6 Part 5): errors-only rate limiting — on a 401/403
-- response, bump this client IP's counter for any matching login/OTP error-rate
-- limit. No-op for zones without errors-only limits. Runs after the response.
require("brisk").log()
